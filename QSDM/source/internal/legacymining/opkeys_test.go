package legacymining

// HL2 WP-C: operator keys (design §2 M1) with real ML-DSA-87 keys. The
// signing side is the miner's own code path (v2client.BuildSignedHMACAttestation
// with the same SignTo call as cmd/qsdmminer-console operator_signer.go).

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	hmacattest "github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
	"github.com/blackbeardONE/QSDM/pkg/mining/v2client"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// opSigner is an owner wallet: an ML-DSA-87 key pair whose address is
// hex(sha256(pk)).
type opSigner struct {
	priv  *mldsa87.PrivateKey
	pub   []byte
	owner string
}

func newOpSigner(t testing.TB) *opSigner {
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
	return &opSigner{priv: priv, pub: raw, owner: hex.EncodeToString(sum[:])}
}

// sign is cmd/qsdmminer-console operatorProofSigner.Sign.
func (s *opSigner) sign(p mining.Proof, b hmacattest.Bundle) (string, error) {
	msg, err := b.CanonicalForOperatorSignature(p)
	if err != nil {
		return "", err
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(s.priv, msg, nil, true, sig); err != nil {
		return "", err
	}
	return hex.EncodeToString(sig), nil
}

// opProof builds a canonical v2 HMAC proof for node by miner the way the
// miner does (v2client); signer nil means no operator_sig. mutate, if set,
// edits the proof after signing (a replayed signature on another proof).
func opProof(t testing.TB, now time.Time, signer *opSigner, miner, node string, mutate func(*mining.Proof)) []byte {
	t.Helper()
	var root, mix, nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	for i := range root {
		root[i], mix[i] = byte(i), byte(0xff-i)
	}
	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Height:     100,
		HeaderHash: [32]byte{0xaa},
		MinerAddr:  miner,
		BatchRoot:  root,
		BatchCount: 1,
		Nonce:      [16]byte{3},
		MixDigest:  mix,
	}
	in := v2client.BundleInputs{
		NodeID: node, GPUUUID: gtGPUUUID, GPUName: gtGPUName,
		ComputeCap: "8.9", CUDAVersion: "12.8", DriverVer: "572.16",
		HMACKey:   gtHMACKy,
		MinerAddr: miner, BatchRoot: root, MixDigest: mix,
		Challenge: challenge.Challenge{Nonce: nonce, IssuedAt: now.Unix(), SignerID: "test-signer", Signature: []byte{1}},
	}
	var att mining.Attestation
	var err error
	if signer != nil {
		att, err = v2client.BuildSignedHMACAttestation(p, in, "ada", signer.sign)
	} else {
		att, err = v2client.BuildHMACAttestation(in, "ada")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := v2client.AttachToProof(&p, att); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&p)
	}
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// opBlock is a block with one signed-envelope tx per (sender, pk) pair.
func opBlock(height uint64, txs ...*mempool.Tx) *chain.Block {
	return &chain.Block{Height: height, Transactions: txs}
}

func opEnrollTx(id, sender string, pk []byte) *mempool.Tx {
	return &mempool.Tx{ID: id, Sender: sender, ContractID: "qsdm/enroll/v2", PublicKey: hex.EncodeToString(pk), Signature: "00"}
}

func TestCheckOperatorKey(t *testing.T) {
	a, b := newOpSigner(t), newOpSigner(t)
	if _, err := CheckOperatorKey(a.owner, a.pub); err != nil {
		t.Fatalf("valid key: %v", err)
	}
	for name, c := range map[string]struct {
		owner string
		pk    []byte
	}{
		"pk of another owner": {a.owner, b.pub},
		"short pk":            {a.owner, a.pub[:100]},
		"uppercase owner":     {strings.ToUpper(a.owner), a.pub},
		"empty owner":         {"", a.pub},
	} {
		if _, err := CheckOperatorKey(c.owner, c.pk); !errors.Is(err, ErrOperatorKey) {
			t.Errorf("%s: %v, want ErrOperatorKey", name, err)
		}
	}
	ks := NewOperatorKeys()
	if _, err := ks.Add(a.owner, b.pub); !errors.Is(err, ErrOperatorKey) {
		t.Fatalf("Add(pk whose hash is not the owner) = %v", err)
	}
	if ks.Len() != 0 {
		t.Fatal("a mismatched key was indexed")
	}
	if added, err := ks.Add(a.owner, a.pub); !added || err != nil {
		t.Fatalf("Add = %v, %v", added, err)
	}
	if added, err := ks.Add(a.owner, a.pub); added || err != nil {
		t.Fatalf("second Add = %v, %v", added, err)
	}
}

func TestOperatorKeysFromBlocks(t *testing.T) {
	a, b, c := newOpSigner(t), newOpSigner(t), newOpSigner(t)
	blocks := []*chain.Block{
		opBlock(5, opEnrollTx("t1", a.owner, b.pub)), // pk does not hash to the sender: ignored
		opBlock(7,
			&mempool.Tx{ID: "t2", Sender: a.owner, ContractID: "qsdm/wallet-transfer/v1"}, // no pk
			opEnrollTx("t3", a.owner, a.pub)),
		opBlock(9, opEnrollTx("t4", a.owner, a.pub), opEnrollTx("t5", b.owner, b.pub)), // a again: first wins
		opBlock(11, &mempool.Tx{ID: "t6", Sender: c.owner, ContractID: "qsdm/tasks/v1", PublicKey: hex.EncodeToString(c.pub)}),
		nil,
	}
	got := OperatorKeysFromBlocks(blocks, nil)
	if len(got) != 3 || got[0].Owner != a.owner || got[0].Height != 7 || got[0].Source != "chain:qsdm/enroll/v2:t3" ||
		got[1].Owner != b.owner || got[2].Owner != c.owner || got[2].Source != "chain:qsdm/tasks/v1:t6" {
		t.Fatalf("got %+v", got)
	}
	only := OperatorKeysFromBlocks(blocks, func(o string) bool { return o == b.owner })
	if len(only) != 1 || only[0].Owner != b.owner {
		t.Fatalf("filtered: %+v", only)
	}
}

func TestVerifyOperatorSig(t *testing.T) {
	a, b := newOpSigner(t), newOpSigner(t)
	pkA, err := CheckOperatorKey(a.owner, a.pub)
	if err != nil {
		t.Fatal(err)
	}
	parse := func(raw []byte) (hmacattest.Bundle, *mining.Proof) {
		t.Helper()
		p, err := mining.ParseProof(raw)
		if err != nil {
			t.Fatal(err)
		}
		bd, err := hmacattest.ParseBundle(p.Attestation.BundleBase64)
		if err != nil {
			t.Fatal(err)
		}
		return bd, p
	}
	bd, p := parse(opProof(t, gtStart, a, a.owner, "n", nil))
	if err := VerifyOperatorSig(bd, p, pkA); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cases := map[string]struct {
		raw  []byte
		want error
	}{
		"unsigned":         {opProof(t, gtStart, nil, a.owner, "n", nil), ErrOperatorSigMissing},
		"other key":        {opProof(t, gtStart, b, a.owner, "n", nil), ErrOperatorSigInvalid},
		"moved mix digest": {opProof(t, gtStart, a, a.owner, "n", func(p *mining.Proof) { p.MixDigest[0] ^= 1 }), ErrOperatorSigInvalid},
		"moved height":     {opProof(t, gtStart, a, a.owner, "n", func(p *mining.Proof) { p.Height++ }), ErrOperatorSigInvalid},
	}
	for name, c := range cases {
		bd, p := parse(c.raw)
		if err := VerifyOperatorSig(bd, p, pkA); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	bd.OperatorSig = "zz" + bd.OperatorSig[2:]
	if err := VerifyOperatorSig(bd, p, pkA); !errors.Is(err, ErrOperatorSigInvalid) {
		t.Errorf("non-hex sig: %v", err)
	}
	bd.OperatorSig = "abcd"
	if err := VerifyOperatorSig(bd, p, pkA); !errors.Is(err, ErrOperatorSigInvalid) {
		t.Errorf("short sig: %v", err)
	}
}

// The design's WP-C acceptance (HL2 §4 row C): a forger who knows the
// victim's (public) HMAC key forges bundles for the victim's node. With the
// operator_sig check each forgery is rejected in Precheck, before rate
// accounting: the victim's bucket and cooldowns are untouched and nothing
// latches. The victim's own signed bundles are accepted.
func TestV2ForgeryRejectedByOwnerAuth(t *testing.T) {
	victim, forger, keyless := newOpSigner(t), newOpSigner(t), newOpSigner(t)
	view := newOTView().
		add("victim-1", victim.owner, true).
		add("forger-1", forger.owner, true).
		add("keyless-1", keyless.owner, true)
	keys := NewOperatorKeys()
	found := OperatorKeysFromBlocks([]*chain.Block{
		opBlock(3, opEnrollTx("e1", victim.owner, victim.pub), opEnrollTx("e2", forger.owner, forger.pub)),
		// The forger publishes its own key under the victim's address: the
		// hash binding refuses it.
		opBlock(4, opEnrollTx("e3", victim.owner, forger.pub), opEnrollTx("e4", keyless.owner, forger.pub)),
	}, nil)
	if rep, err := HydrateOperatorKeys(keys, nil, found); err != nil || rep.Total != 2 {
		t.Fatalf("hydrate: %+v, %v", rep, err)
	}
	cfg := otPublicConfig()
	e := otNew(t, gtDir(t), cfg, view, otOptions{auth: OperatorSigAuth(keys)})
	e.open(t)

	now := e.clock.Now()
	forgeries := map[string][]byte{
		"unsigned":             opProof(t, now, nil, victim.owner, "victim-1", nil),
		"signed by the forger": opProof(t, now, forger, victim.owner, "victim-1", nil),
		// A replayed victim signature on a different proof.
		"victim sig on new proof": opProof(t, now, victim, victim.owner, "victim-1", func(p *mining.Proof) { p.Nonce[0] ^= 0xff }),
	}
	for i := 0; i < 5*DuplicateLimit; i++ {
		for name, raw := range forgeries {
			_, err := e.submitRaw(raw)
			wantKind(t, name, err, KindBadOperatorSig)
			var re *mining.RejectError
			if !errors.As(err, &re) || re.Reason != mining.ReasonAttestation || RejectKindOf(err).HTTPStatus() != 400 {
				t.Fatalf("%s is not a 400 attestation rejection: %v", name, err)
			}
		}
	}
	if e.tokens(victim.owner) != -1 || e.cooling(victim.owner) || e.tokens(forger.owner) != -1 {
		t.Fatal("forgeries created or touched owner state")
	}
	e.requireNoLatch(t)

	// An owner with no ML-DSA key known (its only "key" hashed to someone
	// else) cannot be admitted, even with a signature.
	_, err := e.submitRaw(opProof(t, now, keyless, keyless.owner, "keyless-1", nil))
	wantKind(t, "owner without a key", err, KindBadOperatorSig)

	// The victim still has its whole bucket for its own signed proofs.
	for i := 0; i < cfg.MaxProofsPerMinPerOwner; i++ {
		c, err := e.submitRaw(opProof(t, e.clock.Now(), victim, victim.owner, "victim-1", nil))
		if err != nil || c.Owner != victim.owner {
			t.Fatalf("victim submission %d: %+v, %v", i, c, err)
		}
	}
	_, err = e.submitRaw(opProof(t, e.clock.Now(), victim, victim.owner, "victim-1", nil))
	wantKind(t, "victim over its own rate", err, KindOwnerRateLimited)
	// The forger's own signed proofs for its own node are fine.
	if c, err := e.submitRaw(opProof(t, e.clock.Now(), forger, forger.owner, "forger-1", nil)); err != nil || c.Owner != forger.owner {
		t.Fatalf("forger's own node: %+v, %v", c, err)
	}
	st := e.g.Stats()
	if st.Rejections[KindBadOperatorSig.String()] != uint64(3*5*DuplicateLimit+1) {
		t.Fatalf("stats %+v", st)
	}
}

// The operator_keys table survives a restart, a key in it whose hash is not
// its owner is skipped, and a restart with no chain keys still has them.
func TestOperatorKeysPersistAcrossRestart(t *testing.T) {
	requireHLSQLite(t)
	a, b := newOpSigner(t), newOpSigner(t)
	dir := stDir(t)
	s := NewSQLiteStoreV2()
	s.now = func() time.Time { return stClock }
	m := stMeta
	if err := s.Open(stPath(dir), &m); err != nil {
		t.Fatal(err)
	}
	if v := s.SchemaVersion(); v != StoreUserVersionOperatorKeys {
		t.Fatalf("new v2 store has version %d", v)
	}
	keys := NewOperatorKeys()
	found := OperatorKeysFromBlocks([]*chain.Block{opBlock(9, opEnrollTx("e", a.owner, a.pub))}, nil)
	rep, err := HydrateOperatorKeys(keys, s, found)
	if err != nil || rep.Persisted != 1 || rep.Total != 1 {
		t.Fatalf("first boot: %+v, %v", rep, err)
	}
	if _, err := s.PutOperatorKeys([]OperatorKey{{Owner: a.owner, PublicKey: b.pub, Source: "x"}}); !errors.Is(err, ErrOperatorKey) {
		t.Fatalf("PutOperatorKeys(pk of another owner) = %v", err)
	}
	if n, err := s.PutOperatorKeys(found); n != 0 || err != nil {
		t.Fatalf("re-put = %d, %v", n, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Tamper: a row whose key hashes to someone else, bypassing the store.
	sum := sha256.Sum256([]byte("someone"))
	stRawExec(t, stPath(dir), `INSERT INTO operator_keys (owner, public_key, source, height, added_ns) VALUES ('`+
		hex.EncodeToString(sum[:])+`', x'`+hex.EncodeToString(b.pub)+`', 'tampered', 0, 1)`)

	// Second boot: no chain keys at all.
	s2 := NewSQLiteStoreV2()
	t.Cleanup(func() { _ = s2.Close() })
	if err := s2.Open(stPath(dir), nil); err != nil {
		t.Fatal(err)
	}
	keys2 := NewOperatorKeys()
	rep, err = HydrateOperatorKeys(keys2, s2, nil)
	if err != nil || rep.FromStore != 1 || rep.BadRows != 1 || rep.Total != 1 {
		t.Fatalf("second boot: %+v, %v", rep, err)
	}
	if _, ok := keys2.Lookup(a.owner); !ok {
		t.Fatal("restart lost the key")
	}
	auth := OperatorSigAuth(keys2)
	p, err := mining.ParseProof(opProof(t, gtStart, a, a.owner, "n", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth(p, "n", a.owner); err != nil {
		t.Fatalf("auth after restart: %v", err)
	}
	if err := auth(p, "n", hex.EncodeToString(sum[:])); RejectKindOf(err) != KindBadOperatorSig {
		t.Fatalf("tampered row authorised: %v", err)
	}
	// Rows are immutable.
	if _, err := s2.db.Exec(`UPDATE operator_keys SET source='x'`); err == nil {
		t.Fatal("operator_keys row updated")
	}
	if _, err := s2.db.Exec(`DELETE FROM operator_keys`); err == nil {
		t.Fatal("operator_keys row deleted")
	}
}

// opHL1Fixture creates an HL1 (version 1) DB with one config window and one
// accepted proof, the way an HL1 binary does, and returns its path.
func opHL1Fixture(t *testing.T) (string, Record) {
	t.Helper()
	dir := stDir(t)
	s := stCreate(t, dir)
	stWindow(t, s, stCfg1, 101)
	rec := stRecord(t, 1, stNonce(1))
	if err := s.Accept(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return stPath(dir), rec
}

func opOpen(t *testing.T, s *SQLiteStore, path string) *SQLiteStore {
	t.Helper()
	s.now = func() time.Time { return stClock }
	if err := s.Open(path, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreMigrationFromHL1(t *testing.T) {
	requireHLSQLite(t)
	path, rec := opHL1Fixture(t)
	before := stFileSum(t, path)

	// A version 1 store (HL1, and HL2 with a v1 config) never migrates.
	s := opOpen(t, NewSQLiteStore(), path)
	if v := s.SchemaVersion(); v != StoreUserVersion {
		t.Fatalf("v1 store: version %d", v)
	}
	if _, err := s.OperatorKeys(); !errors.Is(err, ErrSchema) {
		t.Fatalf("OperatorKeys on v1 = %v", err)
	}
	if _, err := s.PutOperatorKeys(nil); !errors.Is(err, ErrSchema) {
		t.Fatalf("PutOperatorKeys on v1 = %v", err)
	}
	_ = s.Close()
	if stFileSum(t, path) != before {
		t.Fatal("a version 1 store changed the HL1 DB")
	}

	// The v2 store migrates, keeps every row, and logs one events row.
	s = opOpen(t, NewSQLiteStoreV2(), path)
	if v := s.SchemaVersion(); v != StoreUserVersionOperatorKeys {
		t.Fatalf("v2 store: version %d", v)
	}
	got, err := s.Lookup([]ProofID{rec.ProofID})
	if err != nil || got[rec.ProofID].MinerAddr != rec.MinerAddr {
		t.Fatalf("proof row lost: %v, %v", got, err)
	}
	if c, err := s.Counters(stCfg1); err != nil || c.Window.FirstHeight != 101 {
		t.Fatalf("counters %+v, %v", c, err)
	}
	if n := stEventCount(t, s, storeEventSchemaMigrate); n != 1 {
		t.Fatalf("%d migrate events", n)
	}
	if ks, err := s.OperatorKeys(); err != nil || len(ks) != 0 {
		t.Fatalf("operator_keys: %v, %v", ks, err)
	}
	_ = s.Close()
	if hdr := opHeaderVersion(t, path); hdr != StoreUserVersionOperatorKeys {
		t.Fatalf("header user_version %d after migration", hdr)
	}

	// Idempotent: a second v2 open changes nothing.
	s = opOpen(t, NewSQLiteStoreV2(), path)
	if n := stEventCount(t, s, storeEventSchemaMigrate); n != 1 {
		t.Fatalf("%d migrate events after reopen", n)
	}
	_ = s.Close()

	// A version 1 store (rollback of the config on an HL2 binary, hl-audit)
	// opens the version 2 DB without changing it.
	after := stFileSum(t, path)
	s = opOpen(t, NewSQLiteStore(), path)
	if v := s.SchemaVersion(); v != StoreUserVersionOperatorKeys {
		t.Fatalf("v1 store on a v2 DB: version %d", v)
	}
	if _, err := s.Pending(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if stFileSum(t, path) != after {
		t.Fatal("a version 1 store changed the version 2 DB")
	}
}

// A migration that fails before its commit leaves the HL1 DB at version 1,
// intact; the next open migrates.
func TestStoreMigrationAbortIsAtomic(t *testing.T) {
	requireHLSQLite(t)
	path, rec := opHL1Fixture(t)
	s := NewSQLiteStoreV2()
	s.now = func() time.Time { return stClock }
	s.migrateHook = func() error { return errors.New("crash before commit") }
	if err := s.Open(path, nil); err == nil {
		_ = s.Close()
		t.Fatal("open succeeded")
	}
	if hdr := opHeaderVersion(t, path); hdr != StoreUserVersion {
		t.Fatalf("header user_version %d", hdr)
	}
	v1 := opOpen(t, NewSQLiteStore(), path)
	if v := v1.SchemaVersion(); v != StoreUserVersion {
		t.Fatalf("after abort: version %d", v)
	}
	if n := stEventCount(t, v1, storeEventSchemaMigrate); n != 0 {
		t.Fatalf("%d migrate events after abort", n)
	}
	_ = v1.Close()
	s2 := opOpen(t, NewSQLiteStoreV2(), path)
	got, err := s2.Lookup([]ProofID{rec.ProofID})
	if s2.SchemaVersion() != StoreUserVersionOperatorKeys || err != nil || len(got) != 1 {
		t.Fatalf("retry: version %d rows %d err %v", s2.SchemaVersion(), len(got), err)
	}
}

// A crash after the migration commit but before the WAL checkpoint leaves
// the main file's header at version 1 and the commit in the WAL. Both store
// kinds open that state as version 2, and nothing migrates twice.
func TestStoreMigrationCrashAfterCommit(t *testing.T) {
	requireHLSQLite(t)
	path, _ := opHL1Fixture(t)
	db, err := storeOpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA wal_autocheckpoint = 0`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := storeMigrateV2Tx(tx, 99, nil); err != nil {
		t.Fatal(err)
	}
	// Snapshot the files while the connection is open: the image a SIGKILL
	// leaves behind.
	crash := filepath.Join(stDir(t), DBFile)
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		stWriteFile(t, crash+suffix, b)
	}
	if hdr := opHeaderVersion(t, crash); hdr != StoreUserVersion {
		t.Skipf("main file already checkpointed (header %d); cannot stage the crash", hdr)
	}
	for name, mk := range map[string]func() *SQLiteStore{"v1 store": NewSQLiteStore, "v2 store": NewSQLiteStoreV2} {
		s := opOpen(t, mk(), crash)
		if v := s.SchemaVersion(); v != StoreUserVersionOperatorKeys {
			t.Fatalf("%s: version %d", name, v)
		}
		if n := stEventCount(t, s, storeEventSchemaMigrate); n != 1 {
			t.Fatalf("%s: %d migrate events", name, n)
		}
		_ = s.Close()
	}
}

func TestStoreSchemaVersionRefusals(t *testing.T) {
	requireHLSQLite(t)
	for name, stmts := range map[string][]string{
		"user_version 3":                {`PRAGMA user_version = 3`},
		"v1 schema with user_version 2": {`PRAGMA user_version = 2`},
		"v2 table with user_version 1":  {storeSchemaOperatorKeys[0].sql},
	} {
		t.Run(name, func(t *testing.T) {
			dir := stDir(t)
			stCreateAt(t, stPath(dir))
			stRawExec(t, stPath(dir), stmts...)
			for _, s := range []*SQLiteStore{NewSQLiteStore(), NewSQLiteStoreV2()} {
				if err := s.Open(stPath(dir), nil); !errors.Is(err, ErrSchema) {
					_ = s.Close()
					t.Fatalf("Open = %v, want ErrSchema", err)
				}
			}
		})
	}
}

func opHeaderVersion(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 100 {
		t.Fatalf("header: %v", err)
	}
	return int(b[60])<<24 | int(b[61])<<16 | int(b[62])<<8 | int(b[63])
}

// Per-submission cost of the M1 check. VerifyOperatorSig is the ML-DSA-87
// verify plus the canonical form; OperatorSigAuth adds the key lookup and
// the second bundle parse.
func BenchmarkVerifyOperatorSig(b *testing.B) {
	a := newOpSigner(b)
	pk, err := CheckOperatorKey(a.owner, a.pub)
	if err != nil {
		b.Fatal(err)
	}
	p, err := mining.ParseProof(opProof(b, gtStart, a, a.owner, "n", nil))
	if err != nil {
		b.Fatal(err)
	}
	bd, err := hmacattest.ParseBundle(p.Attestation.BundleBase64)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := VerifyOperatorSig(bd, p, pk); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOperatorSigAuth(b *testing.B) {
	a := newOpSigner(b)
	keys := NewOperatorKeys()
	if _, err := keys.Add(a.owner, a.pub); err != nil {
		b.Fatal(err)
	}
	auth := OperatorSigAuth(keys)
	p, err := mining.ParseProof(opProof(b, gtStart, a, a.owner, "n", nil))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := auth(p, "n", a.owner); err != nil {
			b.Fatal(err)
		}
	}
}
