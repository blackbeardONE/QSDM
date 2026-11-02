package legacymining

// opkeys.go (HL2 WP-C, design §2 M1): owner-signed submissions.
//
// Every enrolled owner is sha256(ML-DSA-87 public key) (hl1/HL2_OWNER_KEYS.md:
// all 42 owners, via their signed qsdm/enroll/v2 txs). A version 2 Guard with
// require_operator_sig verifies the bundle's operator_sig (the existing
// nvidia-hmac-v1 operator-signature rail, Bundle.CanonicalForOperatorSignature)
// against the key of the enrollment owner, before any per-owner accounting.
// The HMAC keys are public chain state; the owner's ML-DSA private key is not,
// so a forged bundle for a victim's node is unattributable and rejected
// (KindBadOperatorSig, 400) without touching the victim's buckets or
// cooldowns.
//
// Keys come from chain history (design option i): any tx whose public_key
// hashes to its sender. The binding owner == hex(sha256(pk)) is checked for
// every key from every source, so neither the tx signature nor the source
// needs to be trusted. There is no registration endpoint (option ii is not
// needed while every owner has a key on chain).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	hmacattest "github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// The contract constant must match the scheme.
var _ [OperatorKeySize - mldsa87.PublicKeySize]struct{}
var _ [mldsa87.PublicKeySize - OperatorKeySize]struct{}

// OperatorSigSize is the size of an ML-DSA-87 signature (operator_sig is its
// hex encoding).
const OperatorSigSize = mldsa87.SignatureSize

var (
	// ErrOperatorSigMissing: the bundle carries no operator_sig.
	ErrOperatorSigMissing = errors.New("legacymining: operator_sig missing")
	// ErrOperatorSigInvalid: operator_sig is malformed or does not verify.
	ErrOperatorSigInvalid = errors.New("legacymining: operator_sig invalid")
)

// CheckOperatorKey checks that owner is 64 lowercase hex, pk is an ML-DSA-87
// public key, and owner == hex(sha256(pk)). It returns the parsed key. Errors
// wrap ErrOperatorKey.
func CheckOperatorKey(owner string, pk []byte) (*mldsa87.PublicKey, error) {
	if !storeIsLowerHex64(owner) {
		return nil, fmt.Errorf("%w: owner %q is not 64 lowercase hex", ErrOperatorKey, owner)
	}
	if len(pk) != OperatorKeySize {
		return nil, fmt.Errorf("%w: owner %s: public key is %d bytes, want %d (ML-DSA-87)", ErrOperatorKey, owner, len(pk), OperatorKeySize)
	}
	sum := sha256.Sum256(pk)
	if hex.EncodeToString(sum[:]) != owner {
		return nil, fmt.Errorf("%w: owner %s: sha256(public key) is %x", ErrOperatorKey, owner, sum[:])
	}
	var k mldsa87.PublicKey
	if err := k.UnmarshalBinary(pk); err != nil {
		return nil, fmt.Errorf("%w: owner %s: %v", ErrOperatorKey, owner, err)
	}
	return &k, nil
}

// OperatorKeys is the in-memory owner -> ML-DSA-87 key index the OwnerAuth
// reads on every submission. Keys are only added, never replaced: the
// sha256 binding admits exactly one key per owner. Safe for concurrent use.
type OperatorKeys struct {
	mu   sync.RWMutex
	keys map[string]*mldsa87.PublicKey
}

// NewOperatorKeys returns an empty index.
func NewOperatorKeys() *OperatorKeys {
	return &OperatorKeys{keys: make(map[string]*mldsa87.PublicKey)}
}

// Add checks the pair (CheckOperatorKey) and adds it. It reports whether the
// owner was new.
func (ks *OperatorKeys) Add(owner string, pk []byte) (bool, error) {
	k, err := CheckOperatorKey(owner, pk)
	if err != nil {
		return false, err
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if _, ok := ks.keys[owner]; ok {
		return false, nil
	}
	ks.keys[owner] = k
	return true, nil
}

// Lookup returns owner's key.
func (ks *OperatorKeys) Lookup(owner string) (*mldsa87.PublicKey, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	k, ok := ks.keys[owner]
	return k, ok
}

// Len returns the number of owners with a key.
func (ks *OperatorKeys) Len() int {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return len(ks.keys)
}

// OperatorKeysFromBlocks scans blocks for owner keys: every tx whose
// public_key is OperatorKeySize bytes of hex and hashes to its sender (the
// signed qsdm/enroll/v2 envelope always does; wallet-transfer and task txs of
// ML-DSA wallets do too). want, if non-nil, restricts the scan to the owners
// it accepts (cmd/qsdm passes the enrollment owners). The first key found
// per owner is returned, in block order. The tx signature is not re-verified:
// consensus did that when the block was applied, and the hash binding alone
// fixes the key.
func OperatorKeysFromBlocks(blocks []*chain.Block, want func(owner string) bool) []OperatorKey {
	var out []OperatorKey
	seen := make(map[string]bool)
	for _, b := range blocks {
		if b == nil {
			continue
		}
		for _, tx := range b.Transactions {
			if tx == nil || len(tx.PublicKey) != 2*OperatorKeySize || seen[tx.Sender] || !storeIsLowerHex64(tx.Sender) {
				continue
			}
			if want != nil && !want(tx.Sender) {
				continue
			}
			pk, err := hex.DecodeString(tx.PublicKey)
			if err != nil {
				continue
			}
			if _, err := CheckOperatorKey(tx.Sender, pk); err != nil {
				continue
			}
			seen[tx.Sender] = true
			src := "chain:" + tx.ContractID + ":" + tx.ID
			if len(src) > 256 {
				src = src[:256]
			}
			out = append(out, OperatorKey{Owner: tx.Sender, PublicKey: pk, Source: src, Height: b.Height})
		}
	}
	return out
}

// OperatorKeyReport describes one HydrateOperatorKeys run.
type OperatorKeyReport struct {
	FromStore int // valid rows loaded from operator_keys
	BadRows   int // rows that fail CheckOperatorKey (skipped)
	Found     int // keys passed in (from the chain)
	New       int // found keys that were not loaded from the store
	Persisted int // rows written to operator_keys
	Total     int // owners with a key after the run
}

// HydrateOperatorKeys is the boot step that fills keys (cmd/qsdm runs it after
// S14, before S16): first the store's rows, then found (OperatorKeysFromBlocks),
// and the found keys that were new are written back to the store. store may
// be nil (DB not open): keys then come from found only. A store error is
// returned after the in-memory index is filled as far as possible; the
// caller logs it (the keys are a cache of chain data, and a store fault
// surfaces again at Accept).
func HydrateOperatorKeys(keys *OperatorKeys, store OperatorKeyStore, found []OperatorKey) (OperatorKeyReport, error) {
	var rep OperatorKeyReport
	var errs []error
	if store != nil {
		rows, err := store.OperatorKeys()
		if err != nil {
			errs = append(errs, fmt.Errorf("load operator_keys: %w", err))
		}
		for _, r := range rows {
			if _, err := keys.Add(r.Owner, r.PublicKey); err != nil {
				rep.BadRows++
				continue
			}
			rep.FromStore++
		}
	}
	rep.Found = len(found)
	var fresh []OperatorKey
	for _, k := range found {
		added, err := keys.Add(k.Owner, k.PublicKey)
		if err != nil {
			continue // OperatorKeysFromBlocks only returns checked keys
		}
		if added {
			rep.New++
			fresh = append(fresh, k)
		}
	}
	if store != nil && len(fresh) != 0 && len(errs) == 0 {
		n, err := store.PutOperatorKeys(fresh)
		if err != nil {
			errs = append(errs, fmt.Errorf("persist operator_keys: %w", err))
		}
		rep.Persisted = n
	}
	rep.Total = keys.Len()
	return rep, errors.Join(errs...)
}

// VerifyOperatorSig verifies b.OperatorSig, the hex ML-DSA-87 signature over
// b.CanonicalForOperatorSignature(*p) (empty context), under pk. This is the
// same message and scheme as the consensus rail in pkg/mining/attest/hmac
// (verifyOperatorSignature) and as the miner's signer
// (cmd/qsdmminer-console operator_signer.go, v2client.BuildSignedHMACAttestation).
// The canonical form excludes hmac, operator_sig and the bundle base64, and
// binds the proof identity fields (miner_addr, header hash, batch root, mix
// digest, nonces), so a signature cannot be moved to another proof.
func VerifyOperatorSig(b hmacattest.Bundle, p *mining.Proof, pk *mldsa87.PublicKey) error {
	if p == nil || pk == nil {
		return fmt.Errorf("%w: no proof or key", ErrOperatorSigInvalid)
	}
	if b.OperatorSig == "" {
		return ErrOperatorSigMissing
	}
	if len(b.OperatorSig) != 2*OperatorSigSize {
		return fmt.Errorf("%w: %d hex characters, want %d", ErrOperatorSigInvalid, len(b.OperatorSig), 2*OperatorSigSize)
	}
	sig, err := hex.DecodeString(b.OperatorSig)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOperatorSigInvalid, err)
	}
	msg, err := b.CanonicalForOperatorSignature(*p)
	if err != nil {
		return fmt.Errorf("%w: canonical form: %v", ErrOperatorSigInvalid, err)
	}
	if !mldsa87.Verify(pk, msg, nil, sig) {
		return fmt.Errorf("%w: signature does not verify under the owner's key", ErrOperatorSigInvalid)
	}
	return nil
}

// OperatorSigAuth is the WP-C OwnerAuthFunc: the submission's bundle must
// carry an operator_sig that verifies under owner's key in keys. A missing
// key, a missing signature or a bad one is a KindBadOperatorSig rejection
// (400, attestation). cmd/qsdm installs it when the version 2 config sets
// require_operator_sig; the Guard runs it in Precheck after the enrollment
// lookup and before any per-owner accounting.
func OperatorSigAuth(keys *OperatorKeys) OwnerAuthFunc {
	return func(p *mining.Proof, nodeID, owner string) error {
		if p == nil {
			return &Rejection{Kind: KindBadOperatorSig, Detail: "no proof"}
		}
		pk, ok := keys.Lookup(owner)
		if !ok {
			return &Rejection{Kind: KindBadOperatorSig, Detail: "no ML-DSA-87 operator key is known for the owner"}
		}
		b, err := hmacattest.ParseBundle(p.Attestation.BundleBase64)
		if err != nil {
			return &Rejection{Kind: KindBadOperatorSig, Detail: "bundle: " + err.Error()}
		}
		if b.NodeID != nodeID {
			return &Rejection{Kind: KindBadOperatorSig, Detail: "bundle node_id changed"}
		}
		if err := VerifyOperatorSig(b, p, pk); err != nil {
			return &Rejection{Kind: KindBadOperatorSig, Detail: err.Error()}
		}
		return nil
	}
}
