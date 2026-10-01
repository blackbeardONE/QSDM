package main

// HL2 WP-C wiring: hl1NewCanary with a version 2 config, the EnrollmentView
// adapter over *enrollment.InMemoryState, the HL1 DB migration at S7, and the
// operator-key boot step with real ML-DSA-87 keys.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	hmacattest "github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/v2client"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

type hl2Wallet struct {
	priv  *mldsa87.PrivateKey
	pub   []byte
	owner string
}

func newHL2Wallet(t *testing.T) *hl2Wallet {
	t.Helper()
	pub, priv, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pub.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return &hl2Wallet{priv: priv, pub: raw, owner: hex.EncodeToString(sum[:])}
}

func (w *hl2Wallet) sign(p mining.Proof, b hmacattest.Bundle) (string, error) {
	msg, err := b.CanonicalForOperatorSignature(p)
	if err != nil {
		return "", err
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(w.priv, msg, nil, true, sig); err != nil {
		return "", err
	}
	return hex.EncodeToString(sig), nil
}

var hl2HMACKey = bytes.Repeat([]byte{0x5a}, 32)

// hl2Proof is a v2 HMAC proof built by the miner's v2client path; signer nil
// means no operator_sig.
func hl2Proof(t *testing.T, signer *hl2Wallet, miner, node, gpu string) []byte {
	t.Helper()
	var root, mix, nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	root[0], mix[0] = 1, 2
	p := mining.Proof{Version: mining.ProtocolVersionV2, Height: 10, HeaderHash: [32]byte{0xaa}, MinerAddr: miner, BatchRoot: root, BatchCount: 1, MixDigest: mix}
	in := v2client.BundleInputs{
		NodeID: node, GPUUUID: gpu, GPUName: "NVIDIA GeForce RTX 3050", ComputeCap: "8.6", CUDAVersion: "12.8", DriverVer: "572.16",
		HMACKey: hl2HMACKey, MinerAddr: miner, BatchRoot: root, MixDigest: mix,
		Challenge: challenge.Challenge{Nonce: nonce, IssuedAt: time.Now().Unix(), SignerID: "s", Signature: []byte{1}},
	}
	var att mining.Attestation
	var err error
	if signer != nil {
		att, err = v2client.BuildSignedHMACAttestation(p, in, "ampere", signer.sign)
	} else {
		att, err = v2client.BuildHMACAttestation(in, "ampere")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := v2client.AttachToProof(&p, att); err != nil {
		t.Fatal(err)
	}
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func hl2Enroll(t *testing.T, st *enrollment.InMemoryState, node, owner, gpu string, stake uint64) {
	t.Helper()
	if err := st.ApplyEnroll(enrollment.EnrollmentRecord{
		NodeID: node, Owner: owner, GPUUUID: gpu, HMACKey: hl2HMACKey, StakeDust: stake,
		BondMode: enrollment.BondModeMiningRewards, RequiredStakeDust: mining.MinEnrollStakeDust, EnrolledAtHeight: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHL2EnrollmentViewIndex(t *testing.T) {
	st := enrollment.NewInMemoryState()
	a, b := "aa"+hex.EncodeToString(make([]byte, 31)), "bb"+hex.EncodeToString(make([]byte, 31))
	hl2Enroll(t, st, "a-1", a, "GPU-a1", mining.MinEnrollStakeDust)
	v := newHL2EnrollmentView(st)
	if got := v.OwnerNodes(a); len(got) != 1 || !got[0].FullyBonded || !got[0].Active {
		t.Fatalf("OwnerNodes(a) = %+v", got)
	}
	// A node added after the first build is indexed (count change).
	hl2Enroll(t, st, "a-2", a, "GPU-a2", 0)
	hl2Enroll(t, st, "b-1", b, "GPU-b1", mining.MinEnrollStakeDust)
	if got := v.OwnerNodes(a); len(got) != 2 || got[1].NodeID != "a-2" || got[1].FullyBonded {
		t.Fatalf("OwnerNodes(a) after enroll = %+v", got)
	}
	if e, ok := v.Lookup("b-1"); !ok || e.Owner != b || e.RequiredDust != mining.MinEnrollStakeDust {
		t.Fatalf("Lookup(b-1) = %+v, %v", e, ok)
	}
	if _, ok := v.Lookup("nope"); ok {
		t.Fatal("unknown node found")
	}
	// A revoked node stays listed, inactive.
	if err := st.ApplyUnenroll("a-2", 50); err != nil {
		t.Fatal(err)
	}
	for _, e := range v.OwnerNodes(a) {
		if e.NodeID == "a-2" && e.Active {
			t.Fatal("revoked node reported active")
		}
	}
	if o := v.Owners(); len(o) != 2 || !o[a] || !o[b] {
		t.Fatalf("Owners = %v", o)
	}
}

func hl2LegacyDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), legacymining.LegacyDirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A version 2 boot on an HL1 DB: the Store migrates at S7, the operator keys
// come from the chain, a forged bundle for the victim's node is rejected in
// Precheck, and the victim's own signed bundle is attributed to it. A version
// 1 config keeps the HL1 wiring and does not migrate.
func TestHL2NewCanaryV2Wiring(t *testing.T) {
	victim, forger := newHL2Wallet(t), newHL2Wallet(t)
	st := enrollment.NewInMemoryState()
	hl2Enroll(t, st, "victim-1", victim.owner, "GPU-v1", mining.MinEnrollStakeDust)
	hl2Enroll(t, st, "forger-1", forger.owner, "GPU-f1", mining.MinEnrollStakeDust)

	legacy := hl2LegacyDir(t)
	dbPath := filepath.Join(legacy, legacymining.DBFile)
	// The HL1 DB fixture, created by the version 1 store.
	hl1 := legacymining.NewSQLiteStore()
	if err := hl1.Open(dbPath, &legacymining.Meta{H0: 1, GenesisHash: "g", Funder: "f", Release: "hl1"}); err != nil {
		t.Fatal(err)
	}
	if err := hl1.Close(); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	v1 := legacymining.Config{
		Version: 1, Allowed: []legacymining.AllowEntry{{MinerAddr: victim.owner, NodeID: "victim-1"}},
		MaxProofsPerMin: 6, MaxProofsTotal: 100, MaxPending: 10, BudgetCell: 100, ExpiresUnix: now.Add(time.Hour).Unix(),
	}
	env := legacymining.Env{Mode: legacymining.ModeCanary, DBPath: dbPath}
	p1 := hl1NewCanary(hl1BootConfig{Env: env, Config: v1}, true, chain.NewAccountStore(), st)
	if !p1.enabled() || p1.view != nil || p1.keys != nil {
		t.Fatalf("v1 parts: %+v", p1)
	}
	if err := p1.store.Open(dbPath, nil); err != nil {
		t.Fatal(err)
	}
	if v := p1.store.SchemaVersion(); v != legacymining.StoreUserVersion {
		t.Fatalf("v1 config migrated the DB to %d", v)
	}
	if rep := hl2HydrateOperatorKeys(p1, nil, true); rep != (legacymining.OperatorKeyReport{}) {
		t.Fatalf("v1 hydrate did something: %+v", rep)
	}
	_ = p1.store.Close()

	v2 := legacymining.Config{
		Version:         2,
		Allowed:         []legacymining.AllowEntry{{MinerAddr: victim.owner, NodeID: "victim-1"}, {MinerAddr: forger.owner, NodeID: "forger-1"}},
		MaxProofsPerMin: 60, MaxProofsPerMinPerOwner: 6, MaxProofsTotal: 1000, MaxPending: 100, MaxPendingPerOwner: 10,
		OwnerEpochCapCell: 10, DifficultyBits: 16, RequireOperatorSig: true, RequireFullyBonded: true, BondedSlotCap: 1,
		BudgetCell: 100, ExpiresUnix: now.Add(time.Hour).Unix(),
	}
	parts := hl1NewCanary(hl1BootConfig{Env: env, Config: v2}, true, chain.NewAccountStore(), st)
	if !parts.enabled() || parts.view == nil || parts.keys == nil {
		t.Fatalf("v2 parts: %+v", parts)
	}
	if err := parts.store.Open(dbPath, nil); err != nil { // S7, as Reconcile does
		t.Fatal(err)
	}
	defer parts.store.Close()
	if v := parts.store.SchemaVersion(); v != legacymining.StoreUserVersionOperatorKeys {
		t.Fatalf("v2 store did not migrate: version %d", v)
	}
	blocks := []*chain.Block{{Height: 2, Transactions: []*mempool.Tx{
		{ID: "e1", Sender: victim.owner, ContractID: enrollment.SignedContractID, PublicKey: hex.EncodeToString(victim.pub)},
		{ID: "e2", Sender: forger.owner, ContractID: enrollment.SignedContractID, PublicKey: hex.EncodeToString(forger.pub)},
		{ID: "x", Sender: hex.EncodeToString(make([]byte, 32)), PublicKey: hex.EncodeToString(forger.pub)}, // not an owner
	}}}
	rep := hl2HydrateOperatorKeys(parts, blocks, true)
	if rep.Total != 2 || rep.Persisted != 2 || rep.Found != 2 {
		t.Fatalf("hydrate: %+v", rep)
	}

	g := parts.guard
	for name, tc := range map[string]struct {
		raw  []byte
		want legacymining.RejectKind
	}{
		"unsigned forgery":         {hl2Proof(t, nil, victim.owner, "victim-1", "GPU-v1"), legacymining.KindBadOperatorSig},
		"forger-signed forgery":    {hl2Proof(t, forger, victim.owner, "victim-1", "GPU-v1"), legacymining.KindBadOperatorSig},
		"forger names own address": {hl2Proof(t, forger, forger.owner, "victim-1", "GPU-v1"), legacymining.KindMinerNotAllowed},
	} {
		if _, err := g.Precheck(tc.raw); legacymining.RejectKindOf(err) != tc.want {
			t.Fatalf("%s: %v, want %v", name, err, tc.want)
		}
	}
	c, err := g.Precheck(hl2Proof(t, victim, victim.owner, "victim-1", "GPU-v1"))
	if err != nil || c.Owner != victim.owner {
		t.Fatalf("victim's signed proof: %+v, %v", c, err)
	}

	// Restart: a fresh boot with no chain keys still has both from the DB.
	_ = parts.store.Close()
	again := hl1NewCanary(hl1BootConfig{Env: env, Config: v2}, true, chain.NewAccountStore(), st)
	if err := again.store.Open(dbPath, nil); err != nil {
		t.Fatal(err)
	}
	defer again.store.Close()
	if rep := hl2HydrateOperatorKeys(again, nil, true); rep.FromStore != 2 || rep.Total != 2 {
		t.Fatalf("restart hydrate: %+v", rep)
	}
	if c, err := again.guard.Precheck(hl2Proof(t, victim, victim.owner, "victim-1", "GPU-v1")); err != nil || c.Owner != victim.owner {
		t.Fatalf("after restart: %+v, %v", c, err)
	}
}
