package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/blackbeardONE/QSDM/internal/pulse"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// pulseTxDomain separates PULSE transaction signatures from every other one.
const pulseTxDomain = "QSDM/pulse/tx/v1\x00"

// pulseEpochBlocks matches the legacy-mining owner epoch (8,640 blocks).
const pulseEpochBlocks uint64 = 8640

// lmpTag and lmpIDLen mirror the legacy-mining reward payload (QSDM-LMP1).
const (
	lmpTag   = "QSDM-LMP1"
	lmpIDLen = 32
	lmpMax   = 1024
)

// PULSE errors.
var (
	ErrPulseInactive   = errors.New("pulse: the PULSE rule is not active at this height")
	ErrPulseBadTx      = errors.New("pulse: malformed PULSE transaction")
	ErrPulseSignature  = errors.New("pulse: PULSE transaction signature does not verify")
	ErrPulseUnknown    = errors.New("pulse: pool is not authorized")
	ErrPulseNotMember  = errors.New("pulse: owner is not a member of this pool")
	ErrPulseReplay     = errors.New("pulse: settlement sequence or proof already used")
	ErrPulseMismatch   = errors.New("pulse: settlement does not match the escrow")
	ErrPulseNotDue     = errors.New("pulse: escrowed reward cannot be reclaimed yet")
	ErrPulseBondLocked = errors.New("pulse: a pooled reward must not fill a deferred bond")
)

// PulseAction is the payload of a PULSE transaction.
type PulseAction struct {
	Op       string   `json:"op"`                  // join | leave | settle | reclaim
	Pool     string   `json:"pool,omitempty"`      // join
	Batch    string   `json:"batch,omitempty"`     // settle: hex batch body
	BatchSig string   `json:"batch_sig,omitempty"` // settle: hex ML-DSA-87 signature by the pool key
	Proofs   []string `json:"proofs,omitempty"`    // reclaim: hex proof IDs
}

// PulseTxSigningBytes is what the sender signs: the domain, then a canonical
// JSON of sender, nonce, fee and payload.
func PulseTxSigningBytes(tx *mempool.Tx) []byte {
	body, _ := json.Marshal(struct {
		Sender  string `json:"sender"`
		Nonce   uint64 `json:"nonce"`
		Fee     string `json:"fee"`
		Payload string `json:"payload"`
	}{tx.Sender, tx.Nonce, strconv.FormatFloat(tx.Fee, 'f', -1, 64), string(tx.Payload)})
	return append([]byte(pulseTxDomain), body...)
}

// DecodePulseTx checks shape and signature and returns the action.
func DecodePulseTx(tx *mempool.Tx) (PulseAction, error) {
	var a PulseAction
	if tx == nil || tx.ContractID != PulseContractID {
		return a, ErrPulseBadTx
	}
	if tx.Fee < 0 || tx.Amount != 0 || tx.Recipient != "" || len(tx.Payload) == 0 || len(tx.Payload) > 64<<10 {
		return a, ErrPulseBadTx
	}
	dec := json.NewDecoder(bytes.NewReader(tx.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("%w: %v", ErrPulseBadTx, err)
	}
	pkb, err := hex.DecodeString(tx.PublicKey)
	if err != nil {
		return a, ErrPulseSignature
	}
	pk, err := pulse.ParsePublicKey(pkb)
	if err != nil || string(pulse.AddressOf(pk)) != tx.Sender {
		return a, ErrPulseSignature
	}
	sig, err := hex.DecodeString(tx.Signature)
	if err != nil || !pulse.Verify(pk, "", PulseTxSigningBytes(tx), sig) {
		return a, ErrPulseSignature
	}
	switch a.Op {
	case "join":
		if _, err := pulse.ParseAddress(a.Pool); err != nil || a.Batch != "" || a.BatchSig != "" || len(a.Proofs) != 0 {
			return a, ErrPulseBadTx
		}
	case "leave":
		if a.Pool != "" || a.Batch != "" || a.BatchSig != "" || len(a.Proofs) != 0 {
			return a, ErrPulseBadTx
		}
	case "settle":
		if a.Pool != "" || a.Batch == "" || a.BatchSig == "" || len(a.Proofs) != 0 {
			return a, ErrPulseBadTx
		}
	case "reclaim":
		if a.Pool != "" || a.Batch != "" || a.BatchSig != "" || len(a.Proofs) == 0 || len(a.Proofs) > pulse.MaxBatchProofs {
			return a, ErrPulseBadTx
		}
	default:
		return a, ErrPulseBadTx
	}
	return a, nil
}

// PulseAdmissionChecker is the mempool layer for PULSE txs: refused while the
// rule is unscheduled, otherwise shape and signature must hold. Every other
// contract goes to next. Reward txs to escrow accounts are never admitted
// from outside: only the local producer builds them.
func PulseAdmissionChecker(next func(*mempool.Tx) error) func(*mempool.Tx) error {
	return func(tx *mempool.Tx) error {
		if tx != nil && tx.ContractID == PulseContractID {
			if PulseActivationHeight() == 0 {
				return ErrPulseInactive
			}
			a, err := DecodePulseTx(tx)
			if err != nil {
				return err
			}
			return pulseAdmissionPrecheck(tx.Sender, a)
		}
		if tx != nil && tx.ContractID != MiningRewardContractID {
			if _, _, escrow := ParsePulseEscrow(tx.Recipient); escrow {
				return errors.New("pulse: escrow accounts receive only producer mining rewards")
			}
		}
		if next == nil {
			return nil
		}
		return next(tx)
	}
}

type pulseEscrow struct {
	Pool   string `json:"pool"`
	Owner  string `json:"owner"`
	Dust   uint64 `json:"dust"`
	Height uint64 `json:"height"`
}

// PulseStateStore is the PULSE chain state. It is rebuilt from blocks at boot
// (ApplyHistoricalTx / RecordEscrowHistorical), like the task state.
type PulseStateStore struct {
	mu      sync.RWMutex
	members map[string]string            // owner -> pool
	escrow  map[string]pulseEscrow       // unsettled proof ID -> escrow
	used    map[string]bool              // settled or reclaimed proof IDs (global replay map)
	seq     map[string]uint64            // pool -> last settled sequence
	owed    map[string]map[string]uint64 // pool -> payee -> carried dust
	paid    map[uint64]map[string]uint64 // epoch -> payee -> PULSE dust paid
	stats   PulseStats
}

// PulseStats are cumulative counters (metrics); they are not consensus state.
type PulseStats struct {
	Joins, Leaves, Settlements, Reclaims, EscrowedProofs uint64
	PaidDust, FeeDust, DustToFee                         uint64
}

// NewPulseStateStore returns an empty store.
func NewPulseStateStore() *PulseStateStore {
	return &PulseStateStore{
		members: map[string]string{}, escrow: map[string]pulseEscrow{}, used: map[string]bool{},
		seq: map[string]uint64{}, owed: map[string]map[string]uint64{}, paid: map[uint64]map[string]uint64{},
	}
}

// MemberPool returns the pool an owner has joined.
func (s *PulseStateStore) MemberPool(owner string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.members[owner]
	return p, ok
}

// Members returns the owners that have joined pool, sorted. A pool signs settlements only for these owners: the
// chain refuses a batch with any other weight owner.
func (s *PulseStateStore) Members(pool string) []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for owner, p := range s.members {
		if p == pool {
			out = append(out, owner)
		}
	}
	sort.Strings(out)
	return out
}

// Stats returns the counters.
func (s *PulseStateStore) Stats() PulseStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// Count is non-zero once anything PULSE happened (state roots fold it only then).
func (s *PulseStateStore) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.members) + len(s.escrow) + len(s.used) + len(s.seq)
}

// PulseEscrowView is one unsettled escrowed proof as the pool API shows it.
type PulseEscrowView struct {
	Owner  string `json:"owner"`
	Dust   uint64 `json:"dust"`
	Height uint64 `json:"height"`
}

// EscrowedProofs lists a pool's unsettled escrowed proofs (pool API, audit).
func (s *PulseStateStore) EscrowedProofs(pool string) map[string]PulseEscrowView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]PulseEscrowView{}
	for id, e := range s.escrow {
		if e.Pool == pool {
			out[id] = PulseEscrowView{Owner: e.Owner, Dust: e.Dust, Height: e.Height}
		}
	}
	return out
}

// Owed returns a pool's carried balances.
func (s *PulseStateStore) Owed(pool string) map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]uint64{}
	for k, v := range s.owed[pool] {
		out[k] = v
	}
	return out
}

// LastSeq is a pool's last settled batch sequence.
func (s *PulseStateStore) LastSeq(pool string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq[pool]
}

func decodeLMP(payload []byte) ([]string, error) {
	h := len(lmpTag) + 2
	if len(payload) < h || string(payload[:len(lmpTag)]) != lmpTag {
		return nil, errors.New("pulse: reward payload is not QSDM-LMP1")
	}
	n := int(binary.BigEndian.Uint16(payload[len(lmpTag):h]))
	if n == 0 || n > lmpMax || len(payload) != h+n*lmpIDLen {
		return nil, errors.New("pulse: reward payload length")
	}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = hex.EncodeToString(payload[h+i*lmpIDLen : h+(i+1)*lmpIDLen])
	}
	return ids, nil
}

// CheckEscrowReward validates a reward tx addressed to an escrow account
// before it is applied: the rule active, the pool authorized, the owner a
// member of that pool, and the payload's proof IDs never escrowed or used.
func (s *PulseStateStore) CheckEscrowReward(tx *mempool.Tx, h uint64) error {
	pool, owner, ok := ParsePulseEscrow(tx.Recipient)
	if !ok {
		return nil
	}
	if !PulseActiveAt(h) {
		return ErrPulseInactive
	}
	if _, ok := pulsePool(pool); !ok {
		return ErrPulseUnknown
	}
	ids, err := decodeLMP(tx.Payload)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.members[owner] != pool {
		return ErrPulseNotMember
	}
	for _, id := range ids {
		if _, dup := s.escrow[id]; dup || s.used[id] {
			return ErrPulseReplay
		}
	}
	return nil
}

// RecordEscrow records an applied escrow reward: its dust split over its proof
// IDs (floor each, remainder on the last ID).
func (s *PulseStateStore) RecordEscrow(tx *mempool.Tx, h uint64) error {
	pool, owner, ok := ParsePulseEscrow(tx.Recipient)
	if !ok {
		return nil
	}
	ids, err := decodeLMP(tx.Payload)
	if err != nil {
		return err
	}
	total := balanceToDust(tx.Amount)
	each := total / uint64(len(ids))
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, id := range ids {
		d := each
		if i == len(ids)-1 {
			d = total - each*uint64(len(ids)-1)
		}
		s.escrow[id] = pulseEscrow{Pool: pool, Owner: owner, Dust: d, Height: h}
		s.stats.EscrowedProofs++
	}
	return nil
}

// ApplyEconomicTx applies a PULSE transaction to the state and the accounts.
func (s *PulseStateStore) ApplyEconomicTx(tx *mempool.Tx, accounts *AccountStore, h uint64) error {
	if accounts == nil {
		return errors.New("pulse: nil AccountStore")
	}
	return s.apply(tx, accounts, h)
}

// ApplyTx satisfies StateApplier. PULSE needs the block height, so the bare
// form refuses; the applier calls ApplyEconomicTx with the height.
func (s *PulseStateStore) ApplyTx(*mempool.Tx) error {
	return errors.New("pulse: ApplyTx needs a block height (use ApplyEconomicTx)")
}

// ApplyHistoricalTx replays a PULSE transaction into the state only (the
// account snapshot already holds its effects).
func (s *PulseStateStore) ApplyHistoricalTx(tx *mempool.Tx, h uint64) error {
	return s.apply(tx, nil, h)
}

func (s *PulseStateStore) apply(tx *mempool.Tx, accounts *AccountStore, h uint64) error {
	if !PulseActiveAt(h) {
		return ErrPulseInactive
	}
	a, err := DecodePulseTx(tx)
	if err != nil {
		return err
	}
	snapshot := s.ChainReplayClone().(*PulseStateStore)
	var accSnap *AccountStore
	if accounts != nil {
		accSnap = accounts.Clone()
		if err := accounts.ChargeAndBumpNonceAllowCreate(tx.Sender, tx.Fee, tx.Nonce); err != nil {
			return err
		}
	}
	fail := func(err error) error {
		_ = s.RestoreFromChainReplay(snapshot)
		if accounts != nil {
			accounts.RestoreFrom(accSnap)
		}
		return err
	}
	switch a.Op {
	case "join":
		if _, ok := pulsePool(a.Pool); !ok {
			return fail(ErrPulseUnknown)
		}
		s.mu.Lock()
		s.members[tx.Sender] = a.Pool
		s.stats.Joins++
		s.mu.Unlock()
	case "leave":
		s.mu.Lock()
		if _, ok := s.members[tx.Sender]; !ok {
			s.mu.Unlock()
			return fail(ErrPulseNotMember)
		}
		delete(s.members, tx.Sender)
		s.stats.Leaves++
		s.mu.Unlock()
	case "reclaim":
		if err := s.reclaim(tx.Sender, a.Proofs, accounts, h); err != nil {
			return fail(err)
		}
	case "settle":
		if err := s.settle(tx.Sender, a, accounts, h); err != nil {
			return fail(err)
		}
	}
	return nil
}

func (s *PulseStateStore) reclaim(owner string, proofs []string, accounts *AccountStore, h uint64) error {
	p := PulseParamsSnapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	type move struct {
		from string
		dust uint64
	}
	var moves []move
	for _, id := range proofs {
		e, ok := s.escrow[id]
		if !ok || seen[id] || e.Owner != owner {
			return ErrPulseMismatch
		}
		seen[id] = true
		if h < e.Height || h-e.Height < p.ReclaimAfterBlocks {
			return ErrPulseNotDue
		}
		moves = append(moves, move{PulseEscrowAddress(e.Pool, e.Owner), e.Dust})
	}
	if accounts != nil {
		for _, m := range moves {
			if err := pulseMove(accounts, m.from, owner, m.dust); err != nil {
				return err
			}
		}
	}
	for _, id := range proofs {
		delete(s.escrow, id)
		s.used[id] = true
	}
	s.stats.Reclaims++
	return nil
}

// pulseMove moves dust between accounts. Float balances can sit a rounding
// step under an exact dust sum; a shortfall under one dust is taken as the
// whole balance and only what was debited is credited, so nothing is minted.
func pulseMove(accounts *AccountStore, from, to string, dust uint64) error {
	if dust == 0 {
		return nil
	}
	amt := dustToBalance(dust)
	if acc, ok := accounts.Get(from); ok && acc != nil && acc.Balance < amt && amt-acc.Balance < 1.0/float64(dustPerCELL) {
		amt = acc.Balance
	}
	if amt <= 0 {
		return fmt.Errorf("account %s is empty", from)
	}
	if err := accounts.Debit(from, amt); err != nil {
		return err
	}
	accounts.Credit(to, amt)
	return nil
}

// pulseCarry is the pool's keyless carry account: it holds carried balances.
func pulseCarry(pool string) string { return PulseEscrowAddress(pool, pool) }

func (s *PulseStateStore) settle(sender string, a PulseAction, accounts *AccountStore, h uint64) error {
	params := PulseParamsSnapshot()
	body, err := hex.DecodeString(a.Batch)
	if err != nil {
		return ErrPulseBadTx
	}
	sig, err := hex.DecodeString(a.BatchSig)
	if err != nil {
		return ErrPulseBadTx
	}
	b, err := pulse.DecodeBatch(body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPulseBadTx, err)
	}
	if string(b.PoolID) != sender {
		return fmt.Errorf("%w: the settle sender is not the batch's pool", ErrPulseMismatch)
	}
	e, ok := pulsePool(sender)
	if !ok {
		return ErrPulseUnknown
	}
	pkb, _ := hex.DecodeString(e.PublicKey)
	pk, err := pulse.ParsePublicKey(pkb)
	if err != nil || !pulse.Verify(pk, pulse.BatchDomain, body, sig) {
		return ErrPulseSignature
	}
	maxFee := params.MaxFeeBPS
	if e.MaxFeeBPS != 0 && e.MaxFeeBPS < maxFee {
		maxFee = e.MaxFeeBPS
	}
	if b.FeeBPS > maxFee {
		return fmt.Errorf("%w: fee %d bps above the ceiling %d", ErrPulseMismatch, b.FeeBPS, maxFee)
	}
	if b.Height > h || b.Height < params.ActivationHeight {
		return fmt.Errorf("%w: batch height %d outside %d..%d", ErrPulseMismatch, b.Height, params.ActivationHeight, h)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.Seq <= s.seq[sender] {
		return ErrPulseReplay
	}
	weightOwners := map[string]bool{}
	for _, w := range b.Weights {
		// Every weight owner must be a member of the settling pool at this block: a pool cannot route its
		// members' escrowed rewards to an address that never joined (2026-11-18 report). Refusing (not
		// dropping) keeps this a pure tightening: a node without this check accepts every batch this
		// node accepts, so the producer can enforce it before full nodes upgrade.
		if s.members[string(w.Owner)] != sender {
			return fmt.Errorf("%w: weight owner %s is not a member of this pool", ErrPulseMismatch, w.Owner)
		}
		weightOwners[string(w.Owner)] = true
	}
	var pooled uint64
	type move struct {
		from string
		dust uint64
	}
	var moves []move
	for _, id := range b.ProofIDs {
		if s.used[id] {
			return ErrPulseReplay
		}
		pe, ok := s.escrow[id]
		if !ok || pe.Pool != sender || pe.Height > b.Height {
			return fmt.Errorf("%w: proof %s is not escrowed for this pool by height %d", ErrPulseMismatch, id, b.Height)
		}
		if !weightOwners[pe.Owner] {
			return fmt.Errorf("%w: proof %s belongs to %s, who has no weight in the batch", ErrPulseMismatch, id, pe.Owner)
		}
		if pooled+pe.Dust < pooled {
			return errors.New("pulse: pooled amount overflows")
		}
		pooled += pe.Dust
		moves = append(moves, move{PulseEscrowAddress(pe.Pool, pe.Owner), pe.Dust})
	}
	epoch := h / pulseEpochBlocks
	paid := s.paid[epoch]
	in := pulse.SplitInput{Pooled: pooled, FeeBPS: b.FeeBPS, FeeTo: b.FeeTo, FloorDust: b.FloorDust,
		Weights: b.Weights, Owed: map[pulse.Address]uint64{},
		CapLeft: func(a pulse.Address) uint64 {
			used := paid[string(a)]
			if used >= params.PayeeEpochCapDust {
				return 0
			}
			return params.PayeeEpochCapDust - used
		}}
	for k, v := range s.owed[sender] {
		in.Owed[pulse.Address(k)] = v
	}
	res, err := pulse.Split(in)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPulseMismatch, err)
	}
	if !pulse.Conserved(in, res) {
		return errors.New("pulse: split does not conserve value")
	}
	if accounts != nil {
		carry := pulseCarry(sender)
		for _, m := range moves {
			if err := pulseMove(accounts, m.from, carry, m.dust); err != nil {
				return fmt.Errorf("pulse: escrow %s: %w", m.from, err)
			}
		}
		for _, p := range res.Payouts {
			if err := pulseMove(accounts, carry, string(p.To), p.Amount); err != nil {
				return fmt.Errorf("pulse: carry account: %w", err)
			}
		}
	}
	for _, id := range b.ProofIDs {
		delete(s.escrow, id)
		s.used[id] = true
	}
	s.seq[sender] = b.Seq
	owed := map[string]uint64{}
	for k, v := range res.Owed {
		owed[string(k)] = v
	}
	s.owed[sender] = owed
	if s.paid[epoch] == nil {
		s.paid[epoch] = map[string]uint64{}
	}
	for _, p := range res.Payouts {
		s.paid[epoch][string(p.To)] += p.Amount
		s.stats.PaidDust += p.Amount
	}
	for e := range s.paid {
		if e < epoch {
			delete(s.paid, e)
		}
	}
	s.stats.Settlements++
	s.stats.FeeDust += res.Fee
	s.stats.DustToFee += res.Dust
	return nil
}

// ChainReplayClone deep-copies the store.
func (s *PulseStateStore) ChainReplayClone() ChainReplayApplier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := NewPulseStateStore()
	for k, v := range s.members {
		c.members[k] = v
	}
	for k, v := range s.escrow {
		c.escrow[k] = v
	}
	for k, v := range s.used {
		c.used[k] = v
	}
	for k, v := range s.seq {
		c.seq[k] = v
	}
	for p, m := range s.owed {
		c.owed[p] = map[string]uint64{}
		for k, v := range m {
			c.owed[p][k] = v
		}
	}
	for e, m := range s.paid {
		c.paid[e] = map[string]uint64{}
		for k, v := range m {
			c.paid[e][k] = v
		}
	}
	c.stats = s.stats
	return c
}

// RestoreFromChainReplay replaces the store with a clone's contents.
func (s *PulseStateStore) RestoreFromChainReplay(from ChainReplayApplier) error {
	o, ok := from.(*PulseStateStore)
	if !ok || o == nil {
		return errors.New("pulse: RestoreFromChainReplay expects *PulseStateStore")
	}
	c := o.ChainReplayClone().(*PulseStateStore)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members, s.escrow, s.used, s.seq, s.owed, s.paid, s.stats = c.members, c.escrow, c.used, c.seq, c.owed, c.paid, c.stats
	return nil
}

// StateRoot is a hash of the canonical state (stats excluded).
func (s *PulseStateStore) StateRoot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	type kv struct {
		K string `json:"k"`
		V any    `json:"v"`
	}
	sorted := func(m map[string]any) []kv {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]kv, 0, len(keys))
		for _, k := range keys {
			out = append(out, kv{k, m[k]})
		}
		return out
	}
	conv := func(f func(add func(string, any))) []kv {
		m := map[string]any{}
		f(func(k string, v any) { m[k] = v })
		return sorted(m)
	}
	doc := map[string][]kv{
		"members": conv(func(add func(string, any)) {
			for k, v := range s.members {
				add(k, v)
			}
		}),
		"escrow": conv(func(add func(string, any)) {
			for k, v := range s.escrow {
				add(k, v)
			}
		}),
		"used": conv(func(add func(string, any)) {
			for k := range s.used {
				add(k, true)
			}
		}),
		"seq": conv(func(add func(string, any)) {
			for k, v := range s.seq {
				add(k, v)
			}
		}),
		"owed": conv(func(add func(string, any)) {
			for p, m := range s.owed {
				add(p, conv(func(a2 func(string, any)) {
					for k, v := range m {
						a2(k, v)
					}
				}))
			}
		}),
		"paid": conv(func(add func(string, any)) {
			for e, m := range s.paid {
				add(strconv.FormatUint(e, 10), conv(func(a2 func(string, any)) {
					for k, v := range m {
						a2(k, v)
					}
				}))
			}
		}),
	}
	raw, _ := json.Marshal(doc)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
