package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// The token root is the SHA-256 of the documented listing (QSDM_20.md,
// "State and the state root"), rebuilt here from the public views.
func TestQSDM20StateRootListing(t *testing.T) {
	setQSDM20ActivationForTest(t, 1)
	alice, bob := newQSDM20Wallet(t), newQSDM20Wallet(t)
	store := NewQSDM20StateStore()
	empty := sha256.Sum256([]byte("|domain:19:qsdm/token/v1/state"))
	if got := store.StateRoot(); got != hex.EncodeToString(empty[:]) {
		t.Fatalf("empty root = %s", got)
	}
	apply := func(w qsdm20Wallet, nonce uint64, a QSDM20Action, height uint64) {
		t.Helper()
		if err := store.ApplyHistoricalTx(w.signedTx(t, nonce, 0, a), height); err != nil {
			t.Fatal(err)
		}
	}
	apply(alice, 0, qsdm20Create("AAA", "Alpha", 3, "1000", "600", QSDM20MintOpen, "50"), 4)
	open := QSDM20TokenID(alice.address, 0)
	apply(alice, 1, qsdm20Transfer(open, bob.address, "200"), 5)
	apply(bob, 0, qsdm20Create("BBB", "Beta | two", 0, "9", "9", QSDM20MintNone, ""), 6)
	apply(bob, 1, qsdm20Burn(open, "1"), 6)

	tag := func(b *strings.Builder, k, v string) {
		b.WriteString("|" + k + ":" + strconv.Itoa(len(v)) + ":" + v)
	}
	var b strings.Builder
	tag(&b, "domain", "qsdm/token/v1/state")
	tokens := store.Tokens()
	ids := []string{tokens[0].ID, tokens[1].ID}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	for _, id := range ids {
		tok, _ := store.Token(id)
		tag(&b, "token", tok.ID)
		tag(&b, "symbol", tok.Symbol)
		tag(&b, "name", tok.Name)
		tag(&b, "decimals", strconv.Itoa(int(tok.Decimals)))
		tag(&b, "issuer", tok.Issuer)
		tag(&b, "mint", tok.Mint)
		tag(&b, "mint_limit", tok.MintLimit)
		tag(&b, "max_supply", tok.MaxSupply)
		tag(&b, "minted", tok.Minted)
		tag(&b, "burned", tok.Burned)
		tag(&b, "created_height", strconv.FormatUint(tok.CreatedHeight, 10))
		holders, _ := store.Holders(id)
		addrs := make([]string, 0, len(holders))
		balance := map[string]string{}
		for _, h := range holders {
			addrs = append(addrs, h.Address)
			balance[h.Address] = h.Balance
		}
		for i := 0; i < len(addrs); i++ {
			for j := i + 1; j < len(addrs); j++ {
				if addrs[j] < addrs[i] {
					addrs[i], addrs[j] = addrs[j], addrs[i]
				}
			}
		}
		for _, addr := range addrs {
			tag(&b, "holder", addr)
			tag(&b, "balance", balance[addr])
		}
	}
	want := sha256.Sum256([]byte(b.String()))
	if got := store.StateRoot(); got != hex.EncodeToString(want[:]) {
		t.Fatalf("token root %s is not the SHA-256 of the documented listing (%s)", got, hex.EncodeToString(want[:]))
	}
}

// The root depends only on the state: not on map order, and not on the
// order in which independent actions were applied.
func TestQSDM20StateRootDeterminismAndOrderIndependence(t *testing.T) {
	setQSDM20ActivationForTest(t, 1)
	ws := []qsdm20Wallet{newQSDM20Wallet(t), newQSDM20Wallet(t), newQSDM20Wallet(t), newQSDM20Wallet(t)}
	var txs []*mempool.Tx
	for i, w := range ws {
		txs = append(txs, w.signedTx(t, 0, 0, qsdm20Create("T"+strconv.Itoa(i), "Token "+strconv.Itoa(i), uint8(i), "1000", "1000", QSDM20MintNone, "")))
	}
	// Each creator then sends units of its own token to every other wallet.
	for i, w := range ws {
		id := QSDM20TokenID(w.address, 0)
		nonce := uint64(1)
		for j, to := range ws {
			if i == j {
				continue
			}
			txs = append(txs, w.signedTx(t, nonce, 0, qsdm20Transfer(id, to.address, strconv.Itoa(10*(j+1)))))
			nonce++
		}
	}
	build := func(order []int) string {
		store := NewQSDM20StateStore()
		for _, i := range order {
			if err := store.ApplyHistoricalTx(txs[i], 3); err != nil {
				t.Fatalf("apply %d: %v", i, err)
			}
		}
		root := store.StateRoot()
		for k := 0; k < 25; k++ {
			if again := store.StateRoot(); again != root {
				t.Fatal("state root changed between two reads")
			}
		}
		return root
	}
	forward := make([]int, len(txs))
	for i := range forward {
		forward[i] = i
	}
	// Creates first (any order), then each creator's transfers in nonce
	// order but interleaved differently across creators.
	backward := []int{3, 1, 0, 2}
	for i := len(ws) - 1; i >= 0; i-- {
		for k := 0; k < len(ws)-1; k++ {
			backward = append(backward, len(ws)+i*(len(ws)-1)+k)
		}
	}
	a, b := build(forward), build(backward)
	if a != b {
		t.Fatalf("the root depends on apply order: %s vs %s", a, b)
	}
	// And any change of state changes it.
	store := NewQSDM20StateStore()
	for _, i := range forward {
		_ = store.ApplyHistoricalTx(txs[i], 3)
	}
	extra := ws[0].signedTx(t, uint64(len(ws)), 0, qsdm20Burn(QSDM20TokenID(ws[0].address, 0), "1"))
	if err := store.ApplyHistoricalTx(extra, 3); err != nil {
		t.Fatal(err)
	}
	if store.StateRoot() == a {
		t.Fatal("burning a unit did not change the root")
	}
	// The creation height is state too.
	other := NewQSDM20StateStore()
	if err := other.ApplyHistoricalTx(txs[0], 4); err != nil {
		t.Fatal(err)
	}
	same := NewQSDM20StateStore()
	if err := same.ApplyHistoricalTx(txs[0], 3); err != nil {
		t.Fatal(err)
	}
	if other.StateRoot() == same.StateRoot() {
		t.Fatal("the creation height is not in the root")
	}
}

func TestQSDM20CloneRestoreRoundTrip(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice, bob := r.wallet(1), r.wallet(1)
	id := r.create(alice, qsdm20Create("CLN", "Clone", 0, "100", "40", QSDM20MintIssuer, ""))
	r.do(alice, qsdm20Transfer(id, bob.address, "15"))

	snap := r.tokens.ChainReplayClone().(*QSDM20StateStore)
	root := r.tokens.StateRoot()
	if snap.StateRoot() != root || snap.Count() != 1 {
		t.Fatal("clone differs from its source")
	}
	// Mutating the live store leaves the clone alone, and back.
	r.do(alice, qsdm20MintAction(id, bob.address, "60"))
	r.do(bob, qsdm20Burn(id, "5"))
	if snap.StateRoot() != root {
		t.Fatal("live changes reached the clone")
	}
	snapTx := alice.signedTx(t, 1, 0, qsdm20Transfer(id, bob.address, "1"))
	if err := snap.ApplyHistoricalTx(snapTx, 2); err != nil {
		t.Fatal(err)
	}
	liveRoot := r.tokens.StateRoot()
	if err := r.tokens.RestoreFromChainReplay(snap); err != nil {
		t.Fatal(err)
	}
	if r.tokens.StateRoot() != snap.StateRoot() || r.tokens.StateRoot() == liveRoot {
		t.Fatal("restore did not take the snapshot")
	}
	// The restored store is a copy: changing the snapshot afterwards does
	// not reach it.
	restored := r.tokens.StateRoot()
	if err := snap.ApplyHistoricalTx(alice.signedTx(t, 2, 0, qsdm20Transfer(id, bob.address, "1")), 2); err != nil {
		t.Fatal(err)
	}
	if r.tokens.StateRoot() != restored {
		t.Fatal("restored store aliases the snapshot")
	}
	if err := r.tokens.RestoreFromChainReplay(NewStreamStateStore()); err == nil {
		t.Fatal("restore from another store type was accepted")
	}

	// Through the block applier: clone, diverge, restore.
	awareSnap := r.aware.ChainReplayClone().(*EnrollmentAwareApplier)
	beforeRoot := r.aware.StateRoot()
	r.do(bob, qsdm20Burn(id, "1"))
	if awareSnap.StateRoot() != beforeRoot {
		t.Fatal("applier clone follows live changes")
	}
	if err := r.aware.RestoreFromChainReplay(awareSnap); err != nil {
		t.Fatal(err)
	}
	if r.aware.StateRoot() != beforeRoot {
		t.Fatal("applier restore lost the token state")
	}
	bare := NewEnrollmentAwareApplier(NewAccountStore(), nil)
	bare.SetTaskStateStore(NewTaskStateStore())
	bare.SetStreamStateStore(NewStreamStateStore())
	bare.SetRecoveryCapsuleStateStore(NewRecoveryCapsuleStateStore())
	if err := r.aware.RestoreFromChainReplay(bare); err == nil || !strings.Contains(err.Error(), "QSDM-20") {
		t.Fatalf("restore from an applier without token state: %v", err)
	}
}

// Golden roots computed with the code before QSDM-20 (base 94a2d6b): plain
// transfers and a CELL stream. With the token store wired and active, every
// one is unchanged until the first token exists; then the qsdm-20 segment
// folds in exactly as documented.
func TestQSDM20RootUnchangedUntilFirstToken(t *testing.T) {
	setQSDM20ActivationForTest(t, 1)
	alice := strings.Repeat("a1", 32)
	bob := strings.Repeat("b2", 32)
	carol := strings.Repeat("c3", 32)
	payer := newFuzzKey(41)
	accounts := NewAccountStore()
	accounts.Credit(alice, 100)
	accounts.Credit(payer.address, 20)
	aware := NewEnrollmentAwareApplier(accounts, nil)
	aware.SetTaskStateStore(NewTaskStateStore())
	aware.SetStreamStateStore(NewStreamStateStore())
	aware.SetRecoveryCapsuleStateStore(NewRecoveryCapsuleStateStore())
	tokens := NewQSDM20StateStore()
	aware.SetQSDM20StateStore(tokens)
	aware.SetHeightFn(func() uint64 { return 7 })
	golden := []string{
		"449454402baaa564685de7e12b3b09360c58be962301eeb8acb02be76537b467",
		"2882561d60f2d92219606768c9f7ec7f7387b1f4c2629028ee17d5ddaf503898",
		"cbc155ad9efb161981441d8718bc894be26c70c7f6cf7a70b512f85aa55352e4",
		"64abd5e60f20c99950b970d5bfecafec37f037a23927953318a6e62354426ee7",
		"7c5c8d3daf68ff11abfb32e49f72bef42c5607c1d8237e4a0c28015d510a9cd4",
	}
	if got := aware.StateRoot(); got != golden[0] {
		t.Fatalf("root 0 = %s, want %s", got, golden[0])
	}
	txs := []*mempool.Tx{
		{ID: "golden-1", Sender: alice, Recipient: bob, Amount: 10, Fee: 0.5, Nonce: 0},
		{ID: "golden-2", Sender: bob, Recipient: carol, Amount: 3, Fee: 0.25, Nonce: 0},
		{ID: "golden-3", Sender: alice, Recipient: carol, Amount: 1.5, Fee: 0, Nonce: 1},
	}
	session := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	open := StreamAction{
		ID: "golden-stream-open", Sender: payer.address, StreamID: "golden-stream", Action: StreamActionOpen,
		Provider: carol, ServiceID: "qsdm-vpn", DeviceIDHash: strings.Repeat("d4", 32),
		SessionPublicKey: hex.EncodeToString(session), PriceDust: 2 * DustPerCell, PricePeriodSeconds: 2_592_000,
		BudgetDust: 2 * DustPerCell, MaxActiveSeconds: 2_592_000, ExpiresAt: "2026-09-01T00:00:00Z",
		Nonce: 0, Timestamp: "2026-08-01T00:00:00Z",
	}
	payload, _ := json.Marshal(open)
	txs = append(txs, &mempool.Tx{ID: open.ID, Sender: payer.address, Nonce: 0, Payload: payload,
		ContractID: StreamContractID, Signature: hex.EncodeToString(payer.mustSign(t, payload)),
		PublicKey: hex.EncodeToString(payer.pub)})
	for i, tx := range txs {
		if err := aware.ApplyTx(tx); err != nil {
			t.Fatal(err)
		}
		if got := aware.StateRoot(); got != golden[i+1] {
			t.Fatalf("root %d = %s, want the pre-QSDM-20 root %s", i+1, got, golden[i+1])
		}
	}

	// The first token: root = SHA-256("state:" + root + "\nqsdm-20:" + token root).
	issuer := newQSDM20Wallet(t)
	accounts.Credit(issuer.address, 1)
	before := aware.StateRoot()
	if before == golden[4] {
		t.Fatal("crediting the issuer did not change the account root")
	}
	if err := aware.ApplyTx(issuer.signedTx(t, 0, 0, qsdm20Create("FIRST", "First token", 0, "1", "1", QSDM20MintNone, ""))); err != nil {
		t.Fatal(err)
	}
	accountsOnly := NewEnrollmentAwareApplier(accounts, nil)
	accountsOnly.SetStreamStateStore(aware.StreamStateStore())
	withoutTokens := accountsOnly.StateRoot() // the same state, minus the token segment
	sum := sha256.Sum256([]byte("state:" + withoutTokens + "\nqsdm-20:" + tokens.StateRoot()))
	if got := aware.StateRoot(); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("root with the first token = %s, want %s", got, hex.EncodeToString(sum[:]))
	}
}

// A chain with no token transactions, produced by today's wiring (no token
// store), replays block by block through TryAppendExternalBlock on a node
// with the token store wired: every block's root matches, switched off and
// with an activation height set.
func TestQSDM20ReplayChainWithoutTokensKeepsEveryRoot(t *testing.T) {
	payer, provider := newStreamTestWallet(t), newStreamTestWallet(t)
	owner := newRecoveryTestWallet(t)
	sessionPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	genesis := func(accounts *AccountStore) {
		accounts.Credit("funder", 1000)
		accounts.Credit(payer.address, 10)
		accounts.Credit(owner.address, 1)
	}
	newApplier := func(withTokens bool) (*EnrollmentAwareApplier, *AccountStore) {
		accounts := NewAccountStore()
		genesis(accounts)
		aware := NewEnrollmentAwareApplier(accounts, nil)
		aware.SetTaskStateStore(NewTaskStateStore())
		aware.SetStreamStateStore(NewStreamStateStore())
		aware.SetRecoveryCapsuleStateStore(NewRecoveryCapsuleStateStore())
		if withTokens {
			aware.SetQSDM20StateStore(NewQSDM20StateStore())
		}
		return aware, accounts
	}

	// Produce the chain with the pre-QSDM-20 wiring.
	setQSDM20ActivationForTest(t, 0)
	prodAware, _ := newApplier(false)
	pool := mempool.New(mempool.DefaultConfig())
	producer := NewBlockProducer(pool, prodAware, DefaultProducerConfig())
	prodAware.SetHeightFn(func() uint64 { return producer.TipHeight() + 1 })
	open := openStreamAction(t, payer, provider, sessionPublic, 2*DustPerCell)
	batches := [][]*mempool.Tx{
		{{ID: "replay-1", Sender: "funder", Recipient: "alice", Amount: 5, Fee: 0.1, Nonce: 0}},
		{{ID: "replay-2", Sender: "funder", Recipient: "bob", Amount: 7, Fee: 0, Nonce: 1},
			{ID: "replay-3", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 0.01, Nonce: 0}},
		{signedStreamTx(t, payer, open)},
		{signedRecoveryCapsuleTx(t, owner, "replay-recovery", 0)},
		{{ID: "replay-4", Sender: "bob", Recipient: "funder", Amount: 2, Fee: 0, Nonce: 0}},
	}
	var blocks []*Block
	for i, batch := range batches {
		for _, tx := range batch {
			if err := pool.Add(tx); err != nil {
				t.Fatalf("batch %d: %v", i, err)
			}
		}
		blk, err := producer.ProduceBlock()
		if err != nil {
			t.Fatalf("produce block %d: %v", i, err)
		}
		if len(blk.Transactions) != len(batch) {
			t.Fatalf("block %d dropped transactions", i)
		}
		blocks = append(blocks, blk)
	}

	for _, activation := range []uint64{0, 1, 3} {
		SetQSDM20ActivationHeight(activation)
		aware, _ := newApplier(true)
		follower := NewBlockProducer(mempool.New(mempool.DefaultConfig()), aware, DefaultProducerConfig())
		aware.SetHeightFn(func() uint64 { return follower.TipHeight() + 1 })
		for _, blk := range blocks {
			if err := follower.TryAppendExternalBlock(blk); err != nil {
				t.Fatalf("activation %d: block %d: %v", activation, blk.Height, err)
			}
			if got := aware.StateRoot(); got != blk.StateRoot {
				t.Fatalf("activation %d: block %d root %s, produced %s", activation, blk.Height, got, blk.StateRoot)
			}
		}
		if aware.QSDM20StateStore().Count() != 0 {
			t.Fatal("tokens appeared")
		}
	}
}

// Below the activation height the admission gate refuses a token
// transaction, so this code never puts one in a block. A producer without
// QSDM-20 (no admission layer, no token store) can: it applies the
// transaction as a plain CELL transfer. A follower with QSDM-20 accepts that
// block exactly as before, with no token state, and refuses a block whose
// root claims the token was made.
func TestQSDM20BlocksBelowActivationJudgedAsBefore(t *testing.T) {
	setQSDM20ActivationForTest(t, 100)
	issuer := newQSDM20Wallet(t)
	newNode := func(tokens bool) (*BlockProducer, *mempool.Mempool, *EnrollmentAwareApplier, *AccountStore) {
		accounts := NewAccountStore()
		accounts.Credit("funder", 10)
		accounts.Credit(issuer.address, 1)
		aware := NewEnrollmentAwareApplier(accounts, nil)
		if tokens {
			aware.SetQSDM20StateStore(NewQSDM20StateStore())
		}
		pool := mempool.New(mempool.DefaultConfig())
		bp := NewBlockProducer(pool, aware, DefaultProducerConfig())
		aware.SetHeightFn(func() uint64 { return bp.TipHeight() + 1 })
		return bp, pool, aware, accounts
	}
	create := issuer.signedTx(t, 0, 0, qsdm20Create("EARLY", "Too early", 0, "1", "1", QSDM20MintNone, ""))

	_, gated, gatedAware, _ := newNode(true)
	gated.SetAdmissionChecker(QSDM20AdmissionChecker(gatedAware.NextBlockHeight, nil))
	if err := gated.Add(create); !errors.Is(err, ErrQSDM20Inactive) {
		t.Fatalf("admission below the activation height: %v", err)
	}

	oldProducer, pool, _, _ := newNode(false)
	if err := pool.Add(create); err != nil { // no admission layer, as before QSDM-20
		t.Fatal(err)
	}
	if err := pool.Add(&mempool.Tx{ID: "plain", Sender: "funder", Recipient: "x", Amount: 1, Nonce: 0}); err != nil {
		t.Fatal(err)
	}
	blk, err := oldProducer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(blk.Transactions) != 2 {
		t.Fatalf("block carries %d transactions, want the token transaction and the plain one", len(blk.Transactions))
	}
	legacy := NewAccountStore()
	legacy.Credit("funder", 10)
	legacy.Credit(issuer.address, 1)
	for _, tx := range blk.Transactions {
		if err := legacy.ApplyTx(tx); err != nil {
			t.Fatalf("plain transfer of %s: %v", tx.ID, err)
		}
	}
	if root := NewEnrollmentAwareApplier(legacy, nil).StateRoot(); root != blk.StateRoot {
		t.Fatalf("block root %s; the plain transfers give %s", blk.StateRoot, root)
	}

	follower, _, followerAware, followerAccounts := newNode(true)
	if err := follower.TryAppendExternalBlock(blk); err != nil {
		t.Fatalf("follower refused a block that is valid without QSDM-20: %v", err)
	}
	if followerAware.QSDM20StateStore().Count() != 0 {
		t.Fatal("a block below the activation height made a token")
	}
	if got, want := qsdm20AccountsDump(followerAccounts), qsdm20AccountsDump(legacy); got != want {
		t.Fatalf("follower accounts %s, want %s", got, want)
	}

	// A block whose root says the token was made is refused.
	SetQSDM20ActivationHeight(1)
	_, _, specAware, _ := newNode(true)
	specAware.SetHeightFn(func() uint64 { return 1 })
	if err := specAware.ApplyTx(create); err != nil {
		t.Fatal(err)
	}
	SetQSDM20ActivationHeight(100)
	bad := &Block{Height: 0, Timestamp: time.Unix(1700000000, 0), Transactions: []*mempool.Tx{create},
		StateRoot: specAware.StateRoot(), ProducerID: "peer"}
	bad.Hash = computeBlockHash(bad)
	other, _, otherAware, otherAccounts := newNode(true)
	if err := other.TryAppendExternalBlock(bad); err == nil {
		t.Fatal("follower appended a block whose root claims a token below the activation height")
	}
	if otherAware.QSDM20StateStore().Count() != 0 {
		t.Fatal("refused block left token state behind")
	}
	if acc, _ := otherAccounts.Get(issuer.address); acc.Nonce != 0 {
		t.Fatalf("refused block moved the nonce: %+v", acc)
	}
}
