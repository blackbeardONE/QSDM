package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

const (
	bftSigningJournalVersion      = 1
	maxBFTSigningJournalBytes     = 4 << 20
	maxBFTSigningJournalRecords   = 4096
	bftSigningJournalFileMode     = 0o600
	bftSigningJournalStringMaxLen = 512
)

var (
	// ErrBFTSigningJournalConflict means the local signer already reserved the
	// same consensus tuple for a different value. Callers must fail closed.
	ErrBFTSigningJournalConflict = errors.New("chain: BFT signing journal conflict")
	// ErrBFTSigningJournalUnreserved means a caller tried to record a signed
	// envelope without first durably reserving its signing tuple.
	ErrBFTSigningJournalUnreserved = errors.New("chain: BFT signing journal has no reservation")
	// ErrBFTSigningJournalBindingMismatch means a journal belongs to a
	// different network, chain, signer, or consensus configuration.
	ErrBFTSigningJournalBindingMismatch = errors.New("chain: BFT signing journal binding mismatch")
	// ErrBFTSigningJournalCorrupt means the on-disk journal is malformed,
	// unrecognized, or fails its integrity check. A validator must not treat it
	// as an empty journal.
	ErrBFTSigningJournalCorrupt = errors.New("chain: BFT signing journal corrupt")
	// ErrBFTSigningJournalEnvelopeInvalid means a caller tried to mark an
	// intent signed with a malformed or unauthenticated BFT wire envelope.
	ErrBFTSigningJournalEnvelopeInvalid = errors.New("chain: BFT signing journal envelope invalid")
)

// BFTSigningJournalBinding identifies the one consensus context permitted to
// use a journal. ChainID should be an immutable chain identity such as the
// verified genesis block hash; ConsensusConfigFingerprint must change when a
// consensus rule that changes signing meaning changes.
//
// SignerPublicKeyFingerprint uses the same self-certifying SHA-256 form as a
// QSDM BFT validator address. Keeping both fields makes accidental identity
// substitution explicit during recovery.
type BFTSigningJournalBinding struct {
	NetworkID                  string `json:"network_id"`
	ChainID                    string `json:"chain_id"`
	SignerAddress              string `json:"signer_address"`
	SignerPublicKeyFingerprint string `json:"signer_public_key_fingerprint"`
	ConsensusConfigFingerprint string `json:"consensus_config_fingerprint"`
}

// NewBFTSigningJournalBinding constructs a binding for a local validator
// signer. This only derives identity metadata; it does not create a journal or
// enable durable consensus state.
func NewBFTSigningJournalBinding(networkID, chainID, consensusConfigFingerprint string, signer BFTSigner) (BFTSigningJournalBinding, error) {
	if signer == nil {
		return BFTSigningJournalBinding{}, errors.New("chain: BFT signing journal requires a signer")
	}
	publicKey := signer.GetPublicKey()
	if len(publicKey) == 0 {
		return BFTSigningJournalBinding{}, errors.New("chain: BFT signing journal signer has no public key")
	}
	identity := BFTValidatorAddress(publicKey)
	binding := BFTSigningJournalBinding{
		NetworkID:                  networkID,
		ChainID:                    chainID,
		SignerAddress:              identity,
		SignerPublicKeyFingerprint: identity,
		ConsensusConfigFingerprint: consensusConfigFingerprint,
	}
	if err := binding.validate(); err != nil {
		return BFTSigningJournalBinding{}, err
	}
	return binding, nil
}

func (b BFTSigningJournalBinding) validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"network ID", b.NetworkID},
		{"chain ID", b.ChainID},
		{"signer address", b.SignerAddress},
		{"signer public-key fingerprint", b.SignerPublicKeyFingerprint},
		{"consensus configuration fingerprint", b.ConsensusConfigFingerprint},
	} {
		if err := validateBFTSigningJournalString(field.name, field.value); err != nil {
			return err
		}
	}
	if !isLowerHexFingerprint(b.SignerAddress) {
		return fmt.Errorf("chain: BFT signing journal signer address is not a SHA-256 fingerprint")
	}
	if !isLowerHexFingerprint(b.SignerPublicKeyFingerprint) {
		return fmt.Errorf("chain: BFT signing journal signer public-key fingerprint is not a SHA-256 fingerprint")
	}
	if b.SignerAddress != b.SignerPublicKeyFingerprint {
		return fmt.Errorf("chain: BFT signing journal signer address and public-key fingerprint differ")
	}
	if !isLowerHexFingerprint(b.ConsensusConfigFingerprint) {
		return fmt.Errorf("chain: BFT signing journal consensus configuration fingerprint is not a SHA-256 fingerprint")
	}
	return nil
}

// BFTSigningIntent is the exact consensus meaning reserved before a local
// validator signs. ProposalBodyHash is populated only for a proposal and is
// included in the same digest as SignPropose.
type BFTSigningIntent struct {
	Kind             string `json:"kind"`
	Height           uint64 `json:"height"`
	Round            uint32 `json:"round"`
	Validator        string `json:"validator"`
	BlockHash        string `json:"block_hash"`
	ProposalBodyHash string `json:"proposal_body_hash,omitempty"`
	MembershipRoot   string `json:"membership_root"`
}

// BFTSigningIntentForPropose derives a durable signing intent from a proposal.
func BFTSigningIntentForPropose(message BFTWireProposeMsg) BFTSigningIntent {
	return BFTSigningIntent{
		Kind:             BFTWirePropose,
		Height:           message.Height,
		Round:            message.Round,
		Validator:        message.Proposer,
		BlockHash:        message.BlockHash,
		ProposalBodyHash: proposeBodyHash(message.Block),
		MembershipRoot:   message.MembershipRoot,
	}
}

// BFTSigningIntentForPrevote derives a durable signing intent from a prevote.
func BFTSigningIntentForPrevote(message BFTWirePrevoteMsg) BFTSigningIntent {
	return BFTSigningIntent{
		Kind:           BFTWirePrevote,
		Height:         message.Height,
		Round:          message.Round,
		Validator:      message.Validator,
		BlockHash:      message.BlockHash,
		MembershipRoot: message.MembershipRoot,
	}
}

// BFTSigningIntentForPrecommit derives a durable signing intent from a precommit.
func BFTSigningIntentForPrecommit(message BFTWirePrecommitMsg) BFTSigningIntent {
	return BFTSigningIntent{
		Kind:           BFTWirePrecommit,
		Height:         message.Height,
		Round:          message.Round,
		Validator:      message.Validator,
		BlockHash:      message.BlockHash,
		MembershipRoot: message.MembershipRoot,
	}
}

func (i BFTSigningIntent) validate(binding BFTSigningJournalBinding) (string, error) {
	if i.Kind != BFTWirePropose && i.Kind != BFTWirePrevote && i.Kind != BFTWirePrecommit {
		return "", fmt.Errorf("chain: BFT signing journal unsupported vote kind %q", i.Kind)
	}
	if err := validateBFTSigningJournalString("validator", i.Validator); err != nil {
		return "", err
	}
	if i.Validator != binding.SignerAddress {
		return "", fmt.Errorf("%w: intent validator %q does not match journal signer %q", ErrBFTSigningJournalBindingMismatch, i.Validator, binding.SignerAddress)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"block hash", i.BlockHash},
		{"membership root", i.MembershipRoot},
	} {
		if err := validateBFTSigningJournalString(field.name, field.value); err != nil {
			return "", err
		}
	}
	if i.Kind == BFTWirePropose {
		if i.ProposalBodyHash != "" {
			if err := validateBFTSigningJournalString("proposal body hash", i.ProposalBodyHash); err != nil {
				return "", err
			}
		}
	} else if i.ProposalBodyHash != "" {
		return "", fmt.Errorf("chain: BFT signing journal %s intent must not contain a proposal body hash", i.Kind)
	}
	digest := bftVoteDigest(i.Kind, i.Height, i.Round, i.Validator, i.BlockHash, i.ProposalBodyHash, i.MembershipRoot)
	return hex.EncodeToString(digest), nil
}

type bftSigningJournalKey struct {
	Kind      string
	Height    uint64
	Round     uint32
	Validator string
}

func (i BFTSigningIntent) key() bftSigningJournalKey {
	return bftSigningJournalKey{Kind: i.Kind, Height: i.Height, Round: i.Round, Validator: i.Validator}
}

// BFTSigningJournalRecord is an immutable copy of one local signing
// reservation. Digest is the exact digest that the BFT signer must sign.
// SignedEnvelopeHash records the first observed signed envelope for audit; a
// later signature over the same digest is not a consensus conflict.
type BFTSigningJournalRecord struct {
	Intent             BFTSigningIntent `json:"intent"`
	Digest             string           `json:"digest"`
	ReservedAt         time.Time        `json:"reserved_at"`
	SignedAt           time.Time        `json:"signed_at,omitempty"`
	SignedEnvelopeHash string           `json:"signed_envelope_hash,omitempty"`
}

func (r BFTSigningJournalRecord) validate(binding BFTSigningJournalBinding) error {
	digest, err := r.Intent.validate(binding)
	if err != nil {
		return err
	}
	if r.Digest != digest {
		return fmt.Errorf("chain: BFT signing journal record digest mismatch")
	}
	if r.ReservedAt.IsZero() {
		return fmt.Errorf("chain: BFT signing journal record has no reservation timestamp")
	}
	if r.SignedEnvelopeHash == "" {
		if !r.SignedAt.IsZero() {
			return fmt.Errorf("chain: BFT signing journal record has a signed timestamp without an envelope hash")
		}
		return nil
	}
	if r.SignedAt.IsZero() {
		return fmt.Errorf("chain: BFT signing journal record has an envelope hash without a signed timestamp")
	}
	if r.SignedAt.Before(r.ReservedAt) {
		return fmt.Errorf("chain: BFT signing journal record was signed before it was reserved")
	}
	if !isLowerHexFingerprint(r.SignedEnvelopeHash) {
		return fmt.Errorf("chain: BFT signing journal signed envelope hash is not a SHA-256 fingerprint")
	}
	return nil
}

type bftSigningJournalFile struct {
	Version   int                       `json:"version"`
	Binding   BFTSigningJournalBinding  `json:"binding"`
	Records   []BFTSigningJournalRecord `json:"records"`
	Integrity string                    `json:"integrity"`
}

// BFTSigningJournal is an inactive persistence foundation for local vote
// safety. It is intentionally not wired to BFTExecutor or validator startup.
// A caller must reserve a tuple before it asks an ML-DSA signer to emit a vote.
type BFTSigningJournal struct {
	mu      sync.Mutex
	path    string
	binding BFTSigningJournalBinding
	records map[bftSigningJournalKey]BFTSigningJournalRecord
}

// OpenBFTSigningJournal opens a local journal bound to one validator context.
// A missing journal starts empty; any malformed or mismatched existing journal
// fails closed instead of being silently replaced.
func OpenBFTSigningJournal(path string, binding BFTSigningJournalBinding) (*BFTSigningJournal, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("chain: BFT signing journal path is empty")
	}
	if err := binding.validate(); err != nil {
		return nil, err
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return nil, fmt.Errorf("chain: BFT signing journal parent %q is unavailable: %w", parent, err)
	}
	if !parentInfo.IsDir() {
		return nil, fmt.Errorf("chain: BFT signing journal parent %q is not a directory", parent)
	}

	journal := &BFTSigningJournal{
		path:    path,
		binding: binding,
		records: make(map[bftSigningJournalKey]BFTSigningJournalRecord),
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return journal, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chain: stat BFT signing journal %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("chain: BFT signing journal %q must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("chain: BFT signing journal %q is not a regular file", path)
	}
	if info.Size() > maxBFTSigningJournalBytes {
		return nil, fmt.Errorf("%w: BFT signing journal %q exceeds %d bytes", ErrBFTSigningJournalCorrupt, path, maxBFTSigningJournalBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("chain: read BFT signing journal %q: %w", path, err)
	}
	var stored bftSigningJournalFile
	if err := decodeBFTSigningJournal(raw, &stored); err != nil {
		return nil, fmt.Errorf("%w: parse %q: %v", ErrBFTSigningJournalCorrupt, path, err)
	}
	if err := stored.validate(binding); err != nil {
		return nil, err
	}
	for _, record := range stored.Records {
		journal.records[record.Intent.key()] = record
	}
	if err := os.Chmod(path, bftSigningJournalFileMode); err != nil {
		return nil, fmt.Errorf("chain: protect BFT signing journal %q: %w", path, err)
	}
	return journal, nil
}

// Reserve durably records a local signing intent before a BFT signature is
// created. An existing identical reservation is idempotent; any different
// value for the same kind, height, round, and validator is refused.
func (j *BFTSigningJournal) Reserve(intent BFTSigningIntent) (BFTSigningJournalRecord, bool, error) {
	if j == nil {
		return BFTSigningJournalRecord{}, false, errors.New("chain: BFT signing journal is nil")
	}
	digest, err := intent.validate(j.binding)
	if err != nil {
		return BFTSigningJournalRecord{}, false, err
	}
	key := intent.key()
	j.mu.Lock()
	defer j.mu.Unlock()
	if existing, ok := j.records[key]; ok {
		if existing.Intent == intent && existing.Digest == digest {
			return existing, false, nil
		}
		return BFTSigningJournalRecord{}, false, fmt.Errorf("%w: %s height=%d round=%d validator=%s", ErrBFTSigningJournalConflict, intent.Kind, intent.Height, intent.Round, intent.Validator)
	}
	record := BFTSigningJournalRecord{
		Intent:     intent,
		Digest:     digest,
		ReservedAt: time.Now().UTC(),
	}
	candidate := j.cloneRecordsLocked()
	candidate[key] = record
	if err := j.persistLocked(candidate); err != nil {
		return BFTSigningJournalRecord{}, false, err
	}
	j.records = candidate
	return record, true, nil
}

// MarkSigned records that a reserved intent produced a signed BFT envelope.
// It never permits a changed intent. Re-signing the exact same digest after a
// crash is safe and leaves the first envelope hash intact for audit.
func (j *BFTSigningJournal) MarkSigned(intent BFTSigningIntent, envelope []byte) (BFTSigningJournalRecord, bool, error) {
	if j == nil {
		return BFTSigningJournalRecord{}, false, errors.New("chain: BFT signing journal is nil")
	}
	if len(envelope) == 0 {
		return BFTSigningJournalRecord{}, false, errors.New("chain: BFT signing journal signed envelope is empty")
	}
	digest, err := intent.validate(j.binding)
	if err != nil {
		return BFTSigningJournalRecord{}, false, err
	}
	key := intent.key()
	j.mu.Lock()
	defer j.mu.Unlock()
	record, ok := j.records[key]
	if !ok {
		return BFTSigningJournalRecord{}, false, ErrBFTSigningJournalUnreserved
	}
	if record.Intent != intent || record.Digest != digest {
		return BFTSigningJournalRecord{}, false, fmt.Errorf("%w: %s height=%d round=%d validator=%s", ErrBFTSigningJournalConflict, intent.Kind, intent.Height, intent.Round, intent.Validator)
	}
	if err := validateBFTSigningJournalEnvelope(intent, envelope); err != nil {
		return BFTSigningJournalRecord{}, false, err
	}
	if record.SignedEnvelopeHash != "" {
		return record, false, nil
	}
	sum := sha256.Sum256(envelope)
	record.SignedEnvelopeHash = hex.EncodeToString(sum[:])
	record.SignedAt = time.Now().UTC()
	candidate := j.cloneRecordsLocked()
	candidate[key] = record
	if err := j.persistLocked(candidate); err != nil {
		return BFTSigningJournalRecord{}, false, err
	}
	j.records = candidate
	return record, true, nil
}

// Records returns an ordered defensive snapshot. The journal intentionally
// does not expose a destructive prune operation until durable chain-tip
// verification is wired with the consensus activation path.
func (j *BFTSigningJournal) Records() []BFTSigningJournalRecord {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return bftSigningJournalRecords(j.records)
}

func (j *BFTSigningJournal) cloneRecordsLocked() map[bftSigningJournalKey]BFTSigningJournalRecord {
	clone := make(map[bftSigningJournalKey]BFTSigningJournalRecord, len(j.records)+1)
	for key, record := range j.records {
		clone[key] = record
	}
	return clone
}

func (j *BFTSigningJournal) persistLocked(records map[bftSigningJournalKey]BFTSigningJournalRecord) error {
	stored := bftSigningJournalFile{
		Version: bftSigningJournalVersion,
		Binding: j.binding,
		Records: bftSigningJournalRecords(records),
	}
	raw, err := marshalBFTSigningJournal(stored)
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(j.path, raw, bftSigningJournalFileMode); err != nil {
		return fmt.Errorf("chain: persist BFT signing journal %q: %w", j.path, err)
	}
	if err := os.Chmod(j.path, bftSigningJournalFileMode); err != nil {
		return fmt.Errorf("chain: protect BFT signing journal %q: %w", j.path, err)
	}
	return nil
}

func (f bftSigningJournalFile) validate(binding BFTSigningJournalBinding) error {
	if f.Version != bftSigningJournalVersion {
		return fmt.Errorf("%w: unsupported BFT signing journal version %d", ErrBFTSigningJournalCorrupt, f.Version)
	}
	if err := f.Binding.validate(); err != nil {
		return fmt.Errorf("%w: invalid BFT signing journal binding: %v", ErrBFTSigningJournalCorrupt, err)
	}
	if f.Binding != binding {
		return fmt.Errorf("%w: stored journal belongs to a different validator context", ErrBFTSigningJournalBindingMismatch)
	}
	if len(f.Records) > maxBFTSigningJournalRecords {
		return fmt.Errorf("%w: BFT signing journal has %d records (max %d)", ErrBFTSigningJournalCorrupt, len(f.Records), maxBFTSigningJournalRecords)
	}
	expected, err := bftSigningJournalIntegrity(f)
	if err != nil {
		return fmt.Errorf("%w: compute journal integrity: %v", ErrBFTSigningJournalCorrupt, err)
	}
	if f.Integrity != expected {
		return fmt.Errorf("%w: BFT signing journal integrity mismatch", ErrBFTSigningJournalCorrupt)
	}
	var previous *BFTSigningJournalRecord
	for index := range f.Records {
		record := f.Records[index]
		if err := record.validate(binding); err != nil {
			return fmt.Errorf("%w: record %d: %v", ErrBFTSigningJournalCorrupt, index, err)
		}
		if previous != nil && compareBFTSigningJournalRecord(*previous, record) >= 0 {
			return fmt.Errorf("%w: records are not strictly ordered", ErrBFTSigningJournalCorrupt)
		}
		previous = &f.Records[index]
	}
	return nil
}

func marshalBFTSigningJournal(file bftSigningJournalFile) ([]byte, error) {
	integrity, err := bftSigningJournalIntegrity(file)
	if err != nil {
		return nil, err
	}
	file.Integrity = integrity
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("chain: encode BFT signing journal: %w", err)
	}
	return append(raw, '\n'), nil
}

func bftSigningJournalIntegrity(file bftSigningJournalFile) (string, error) {
	file.Integrity = ""
	raw, err := json.Marshal(file)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func decodeBFTSigningJournal(raw []byte, target *bftSigningJournalFile) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

// validateBFTSigningJournalEnvelope ensures the recorded envelope is a real,
// authenticated BFT message for the exact value already reserved on disk.
// This keeps the journal's audit record tied to the consensus value rather
// than merely proving that arbitrary bytes were observed after a reservation.
func validateBFTSigningJournalEnvelope(intent BFTSigningIntent, envelope []byte) error {
	kind, raw, err := UnmarshalBFTWire(envelope)
	if err != nil {
		return fmt.Errorf("%w: decode BFT wire envelope: %v", ErrBFTSigningJournalEnvelopeInvalid, err)
	}
	if kind != intent.Kind {
		return fmt.Errorf("%w: wire kind %q does not match reserved kind %q", ErrBFTSigningJournalConflict, kind, intent.Kind)
	}

	switch kind {
	case BFTWirePropose:
		var message BFTWireProposeMsg
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("%w: decode proposal: %v", ErrBFTSigningJournalEnvelopeInvalid, err)
		}
		if derived := BFTSigningIntentForPropose(message); derived != intent {
			return fmt.Errorf("%w: proposal does not match its reserved intent", ErrBFTSigningJournalConflict)
		}
		if err := VerifyPropose(message); err != nil {
			return fmt.Errorf("%w: verify proposal: %v", ErrBFTSigningJournalEnvelopeInvalid, err)
		}
	case BFTWirePrevote:
		var message BFTWirePrevoteMsg
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("%w: decode prevote: %v", ErrBFTSigningJournalEnvelopeInvalid, err)
		}
		if derived := BFTSigningIntentForPrevote(message); derived != intent {
			return fmt.Errorf("%w: prevote does not match its reserved intent", ErrBFTSigningJournalConflict)
		}
		if err := VerifyPrevote(message); err != nil {
			return fmt.Errorf("%w: verify prevote: %v", ErrBFTSigningJournalEnvelopeInvalid, err)
		}
	case BFTWirePrecommit:
		var message BFTWirePrecommitMsg
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("%w: decode precommit: %v", ErrBFTSigningJournalEnvelopeInvalid, err)
		}
		if derived := BFTSigningIntentForPrecommit(message); derived != intent {
			return fmt.Errorf("%w: precommit does not match its reserved intent", ErrBFTSigningJournalConflict)
		}
		if err := VerifyPrecommit(message); err != nil {
			return fmt.Errorf("%w: verify precommit: %v", ErrBFTSigningJournalEnvelopeInvalid, err)
		}
	default:
		return fmt.Errorf("%w: unsupported BFT wire kind %q", ErrBFTSigningJournalEnvelopeInvalid, kind)
	}
	return nil
}
func bftSigningJournalRecords(records map[bftSigningJournalKey]BFTSigningJournalRecord) []BFTSigningJournalRecord {
	out := make([]BFTSigningJournalRecord, 0, len(records))
	for _, record := range records {
		out = append(out, record)
	}
	sort.Slice(out, func(left, right int) bool {
		return compareBFTSigningJournalRecord(out[left], out[right]) < 0
	})
	return out
}

func compareBFTSigningJournalRecord(left, right BFTSigningJournalRecord) int {
	for _, comparison := range []int{
		strings.Compare(left.Intent.Kind, right.Intent.Kind),
		strings.Compare(left.Intent.Validator, right.Intent.Validator),
		compareUint64(left.Intent.Height, right.Intent.Height),
		compareUint32(left.Intent.Round, right.Intent.Round),
	} {
		if comparison != 0 {
			return comparison
		}
	}
	return 0
}

func compareUint64(left, right uint64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareUint32(left, right uint32) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func validateBFTSigningJournalString(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("chain: BFT signing journal %s is empty or has surrounding whitespace", name)
	}
	if len(value) > bftSigningJournalStringMaxLen {
		return fmt.Errorf("chain: BFT signing journal %s exceeds %d bytes", name, bftSigningJournalStringMaxLen)
	}
	if strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("chain: BFT signing journal %s contains NUL", name)
	}
	return nil
}

func isLowerHexFingerprint(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
