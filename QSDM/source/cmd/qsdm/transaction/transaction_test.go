package transaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/poe"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/walletp2p"
)

type sliceStorage struct {
	stored [][]byte
}

func txTestDedupeReset(t *testing.T) {
	t.Helper()
	walletp2p.ResetForTest()
	t.Cleanup(walletp2p.ResetForTest)
}

func (s *sliceStorage) StoreTransaction(tx []byte) error {
	s.stored = append(s.stored, append([]byte(nil), tx...))
	return nil
}

func (s *sliceStorage) Close() error { return nil }

var (
	testParent1 = strings.Repeat("a", 32)
	testParent2 = strings.Repeat("b", 32)
)

func addressOf(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// p2pTx describes one signed wallet envelope for the P2P tests.
type p2pTx struct {
	signer  *consensus.ProofOfEntanglement // signs the canonical bytes
	keyFrom *consensus.ProofOfEntanglement // supplies public_key (default signer)
	sender  string                         // default: address of keyFrom
	geoTag  string
	parents []string // default {testParent1, testParent2}
}

func buildP2PTx(t *testing.T, o p2pTx) []byte {
	t.Helper()
	const id32 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if o.keyFrom == nil {
		o.keyFrom = o.signer
	}
	if o.parents == nil {
		o.parents = []string{testParent1, testParent2}
	}
	pub := o.keyFrom.MLDSAPublicKey()
	if o.sender == "" {
		o.sender = addressOf(pub)
	}
	env := Transaction{
		ID:          id32,
		Sender:      o.sender,
		Recipient:   strings.Repeat("2", 64),
		Amount:      1.0,
		Fee:         0.1,
		GeoTag:      o.geoTag,
		ParentCells: o.parents,
		Nonce:       1,
		// Fresh timestamp so MED-3 freshness validation (24h window, 30s
		// future clock-skew) accepts the envelope.
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	body, err := env.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := o.signer.Sign(body)
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = hex.EncodeToString(pub)
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newTestPoE(t *testing.T) *consensus.ProofOfEntanglement {
	t.Helper()
	p := consensus.NewProofOfEntanglement()
	if p == nil {
		t.Skip("ProofOfEntanglement unavailable (no ML-DSA-87 backend)")
	}
	return p
}

func buildP2PTestTxMessage(t *testing.T, signer *consensus.ProofOfEntanglement) []byte {
	return buildP2PTx(t, p2pTx{signer: signer})
}

func buildP2PTestTxMessageWithGeo(t *testing.T, signer *consensus.ProofOfEntanglement, geoTag string) []byte {
	return buildP2PTx(t, p2pTx{signer: signer, geoTag: geoTag})
}

// meshCompanionOf wraps a signed wallet message the way senders do.
func meshCompanionOf(t *testing.T, walletMsg []byte, submeshKey string) []byte {
	t.Helper()
	wire, err := mesh3d.BuildMeshCompanionFromWalletJSON(walletMsg, []string{testParent1, testParent2}, submeshKey)
	if err != nil {
		t.Fatalf("mesh companion: %v", err)
	}
	return wire
}

func TestHandleTransaction_P2PGate_blocksWithoutProof(t *testing.T) {
	txTestDedupeReset(t)
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	signer := newTestPoE(t)
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	st := &sliceStorage{}
	gate := &monitoring.NvidiaLockP2PGate{Enabled: true, MaxProofAge: 24 * time.Hour}

	before := monitoring.NvidiaLockP2PRejectCount()
	msg := buildP2PTestTxMessage(t, signer)
	HandleTransaction(logger, msg, dm, nil, signer, st, gate)

	if len(st.stored) != 0 {
		t.Fatalf("expected no store, got %d", len(st.stored))
	}
	if monitoring.NvidiaLockP2PRejectCount()-before != 1 {
		t.Fatalf("expected one P2P reject, before=%d after=%d", before, monitoring.NvidiaLockP2PRejectCount())
	}
}

func TestHandleTransaction_P2PGate_storesWithQualifyingProof(t *testing.T) {
	txTestDedupeReset(t)
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	raw := []byte(`{"architecture":"NVIDIA test","cuda_proof_hash":"x","gpu_fingerprint":{"available":true}}`)
	if err := monitoring.RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}

	signer := newTestPoE(t)
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	st := &sliceStorage{}
	gate := &monitoring.NvidiaLockP2PGate{Enabled: true, MaxProofAge: 24 * time.Hour}

	msg := buildP2PTestTxMessage(t, signer)
	before := monitoring.NvidiaLockP2PRejectCount()
	HandleTransaction(logger, msg, dm, nil, signer, st, gate)

	if monitoring.NvidiaLockP2PRejectCount() != before {
		t.Fatal("unexpected P2P reject")
	}
	if len(st.stored) != 1 {
		t.Fatalf("expected one store, got %d", len(st.stored))
	}
}

func TestHandleTransaction_P2PGate_nilGateAlwaysStores(t *testing.T) {
	txTestDedupeReset(t)
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	signer := newTestPoE(t)
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	st := &sliceStorage{}
	msg := buildP2PTestTxMessage(t, signer)
	HandleTransaction(logger, msg, dm, nil, signer, st, nil)
	if len(st.stored) != 1 {
		t.Fatalf("expected store, got %d", len(st.stored))
	}
}

// The receiving node's own PoE key must play no part: a transaction signed
// by a different wallet verifies under the wallet's key.
func TestHandleTransaction_VerifiesUnderEnvelopeKeyNotNodeKey(t *testing.T) {
	txTestDedupeReset(t)
	walletKey := newTestPoE(t)
	nodeKey := newTestPoE(t)
	st := &sliceStorage{}
	msg := buildP2PTx(t, p2pTx{signer: walletKey})
	HandleTransaction(logging.NewSilentLogger(), msg, submesh.NewDynamicSubmeshManager(), nil, nodeKey, st, nil)
	if len(st.stored) != 1 {
		t.Fatalf("third-party wallet transaction not stored (got %d)", len(st.stored))
	}
}

func TestHandleTransaction_RejectsWrongSignerKey(t *testing.T) {
	txTestDedupeReset(t)
	attacker := newTestPoE(t)
	victim := newTestPoE(t)
	st := &sliceStorage{}
	// Signed by the attacker, presented with the victim's key and address.
	msg := buildP2PTx(t, p2pTx{signer: attacker, keyFrom: victim})
	HandleTransaction(logging.NewSilentLogger(), msg, submesh.NewDynamicSubmeshManager(), nil, attacker, st, nil)
	if len(st.stored) != 0 {
		t.Fatal("transaction signed by another key was stored")
	}
}

func TestHandleTransaction_RejectsSenderKeyMismatch(t *testing.T) {
	txTestDedupeReset(t)
	attacker := newTestPoE(t)
	victim := newTestPoE(t)
	st := &sliceStorage{}
	// Validly signed by the attacker's key, but spending from the victim.
	msg := buildP2PTx(t, p2pTx{signer: attacker, sender: addressOf(victim.MLDSAPublicKey())})
	HandleTransaction(logging.NewSilentLogger(), msg, submesh.NewDynamicSubmeshManager(), nil, attacker, st, nil)
	if len(st.stored) != 0 {
		t.Fatal("transaction whose sender is not its key's address was stored")
	}
}

func TestHandleTransaction_RejectsMissingPublicKey(t *testing.T) {
	txTestDedupeReset(t)
	signer := newTestPoE(t)
	var env Transaction
	if err := json.Unmarshal(buildP2PTx(t, p2pTx{signer: signer}), &env); err != nil {
		t.Fatal(err)
	}
	env.PublicKey = ""
	msg, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	st := &sliceStorage{}
	HandleTransaction(logging.NewSilentLogger(), msg, submesh.NewDynamicSubmeshManager(), nil, signer, st, nil)
	if len(st.stored) != 0 {
		t.Fatal("transaction without public_key was stored")
	}
}

// Zero parents and one parent fail the context-free PoE rules on this path.
func TestHandleTransaction_RejectsTooFewParents(t *testing.T) {
	for _, parents := range [][]string{{}, {testParent1}} {
		txTestDedupeReset(t)
		signer := newTestPoE(t)
		st := &sliceStorage{}
		msg := buildP2PTx(t, p2pTx{signer: signer, parents: parents})
		HandleTransaction(logging.NewSilentLogger(), msg, submesh.NewDynamicSubmeshManager(), nil, signer, st, nil)
		if len(st.stored) != 0 {
			t.Fatalf("%d parents: stored", len(parents))
		}
	}
}

// The chain-backed parent check runs on both P2P paths and sees the
// envelope's signed ID and parents.
func TestDispatchInboundP2P_ParentCheck(t *testing.T) {
	signer := newTestPoE(t)
	walletMsg := buildP2PTx(t, p2pTx{signer: signer})
	for name, msg := range map[string][]byte{
		"wallet-json":    walletMsg,
		"mesh-companion": meshCompanionOf(t, walletMsg, "route-a"),
	} {
		t.Run(name, func(t *testing.T) {
			for _, reject := range []bool{true, false} {
				txTestDedupeReset(t)
				st := &sliceStorage{}
				var gotID string
				var gotParents []string
				deps := meshDeps(st, signer, submesh.NewDynamicSubmeshManager())
				deps.Msg = msg
				deps.ParentCheck = func(id string, parents []string) error {
					gotID, gotParents = id, parents
					if reject {
						return poe.ErrParentUnknown
					}
					return nil
				}
				DispatchInboundP2P(deps)
				if gotID != "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" || len(gotParents) != 2 || gotParents[0] != testParent1 {
					t.Fatalf("parent check saw id=%q parents=%v", gotID, gotParents)
				}
				want := 1
				if reject {
					want = 0
				}
				if len(st.stored) != want {
					t.Fatalf("reject=%v: stored %d, want %d", reject, len(st.stored), want)
				}
			}
		})
	}
}

func TestHandleTransaction_SubmeshConfigured_rejectsWhenNoRoute(t *testing.T) {
	txTestDedupeReset(t)
	signer := newTestPoE(t)
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	st := &sliceStorage{}
	msg := buildP2PTestTxMessage(t, signer)
	HandleTransaction(logger, msg, dm, nil, signer, st, nil)
	if len(st.stored) != 0 {
		t.Fatalf("expected no store when geotag does not match submesh, got %d", len(st.stored))
	}
}

func TestHandleTransaction_SubmeshConfigured_storesWhenRouteMatches(t *testing.T) {
	txTestDedupeReset(t)
	signer := newTestPoE(t)
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	st := &sliceStorage{}
	msg := buildP2PTestTxMessageWithGeo(t, signer, "US")
	HandleTransaction(logger, msg, dm, nil, signer, st, nil)
	if len(st.stored) != 1 {
		t.Fatalf("expected store, got %d", len(st.stored))
	}
}

func TestEncodeParseMesh3DWire(t *testing.T) {
	txTestDedupeReset(t)
	txData := bytes.Repeat([]byte("d"), 64)
	tx := &mesh3d.Transaction{
		ID: string(bytes.Repeat([]byte("x"), 32)),
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: bytes.Repeat([]byte{1}, 32)},
			{ID: "p2", Data: bytes.Repeat([]byte{2}, 32)},
			{ID: "p3", Data: bytes.Repeat([]byte{3}, 32)},
		},
		Data: txData,
	}
	raw, err := EncodeMesh3DWire(tx, "sm-test")
	if err != nil {
		t.Fatal(err)
	}
	got, sub, err := ParsePhase3Wire(raw)
	if err != nil {
		t.Fatal(err)
	}
	if sub != "sm-test" || got.ID != tx.ID || !bytes.Equal(got.Data, txData) {
		t.Fatalf("round-trip mismatch: sub=%q id=%q data=%d", sub, got.ID, len(got.Data))
	}
	if len(got.ParentCells) != 3 {
		t.Fatalf("parents %d", len(got.ParentCells))
	}
}

func meshDeps(st *sliceStorage, cons *consensus.ProofOfEntanglement, dm *submesh.DynamicSubmeshManager) DispatchDeps {
	return DispatchDeps{
		Logger:            logging.NewSilentLogger(),
		DynamicManager:    dm,
		WasmSdk:           nil,
		Consensus:         cons,
		Storage:           st,
		NvidiaGate:        nil,
		Mesh3dValidator:   mesh3d.NewMesh3DValidator(),
		QuarantineManager: quarantine.NewQuarantineManager(0.5),
		ReputationManager: quarantine.NewReputationManager(10, 5),
	}
}

func TestDispatchInboundP2P_mesh3DWire(t *testing.T) {
	txTestDedupeReset(t)
	signer := newTestPoE(t)
	st := &sliceStorage{}
	walletMsg := buildP2PTx(t, p2pTx{signer: signer})
	deps := meshDeps(st, signer, submesh.NewDynamicSubmeshManager())
	deps.Msg = meshCompanionOf(t, walletMsg, "route-a")
	DispatchInboundP2P(deps)
	if len(st.stored) != 1 || !bytes.Equal(st.stored[0], walletMsg) {
		t.Fatalf("expected mesh payload stored, got %d stored", len(st.stored))
	}
}

// Mesh structure failures were warnings; arbitrary payloads and parents that
// do not match the signed envelope are now dropped.
func TestDispatchInboundP2P_mesh3DWireStructureFailuresDropped(t *testing.T) {
	signer := newTestPoE(t)
	walletMsg := buildP2PTx(t, p2pTx{signer: signer})
	digest := mesh3d.PayloadDigest(walletMsg)
	unsigned := &mesh3d.Transaction{
		ID: string(bytes.Repeat([]byte("y"), 32)),
		ParentCells: []mesh3d.ParentCell{
			{ID: "a1", Data: bytes.Repeat([]byte{1}, 32)},
			{ID: "a2", Data: bytes.Repeat([]byte{2}, 32)},
			{ID: "a3", Data: bytes.Repeat([]byte{3}, 32)},
		},
		Data: bytes.Repeat([]byte("d"), 64),
	}
	rewired := &mesh3d.Transaction{
		ID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		ParentCells: []mesh3d.ParentCell{
			{ID: "other-parent-0000000", Data: mesh3d.ParentDataFor("other-parent-0000000")},
			{ID: testParent2, Data: mesh3d.ParentDataFor(testParent2)},
			{ID: digest, Data: mesh3d.ParentDataFor(digest)},
		},
		Data: walletMsg,
	}
	for name, tx := range map[string]*mesh3d.Transaction{"unsigned-payload": unsigned, "parents-not-signed": rewired} {
		t.Run(name, func(t *testing.T) {
			txTestDedupeReset(t)
			st := &sliceStorage{}
			wire, err := EncodeMesh3DWire(tx, "route-a")
			if err != nil {
				t.Fatal(err)
			}
			deps := meshDeps(st, signer, submesh.NewDynamicSubmeshManager())
			deps.Msg = wire
			DispatchInboundP2P(deps)
			if len(st.stored) != 0 {
				t.Fatal("invalid mesh transaction stored")
			}
		})
	}
}

func TestDispatchInboundP2P_walletJSONNotMesh(t *testing.T) {
	txTestDedupeReset(t)
	signer := newTestPoE(t)
	st := &sliceStorage{}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	deps := meshDeps(st, signer, dm)
	deps.Msg = buildP2PTestTxMessageWithGeo(t, signer, "US")
	DispatchInboundP2P(deps)
	if len(st.stored) != 1 {
		t.Fatalf("expected one wallet tx stored, got %d", len(st.stored))
	}
}

func TestDispatchInboundP2P_dedupeMeshThenWalletJSON(t *testing.T) {
	txTestDedupeReset(t)
	signer := newTestPoE(t)
	st := &sliceStorage{}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	walletMsg := buildP2PTestTxMessageWithGeo(t, signer, "US")
	wire := meshCompanionOf(t, walletMsg, "route-a")
	deps := meshDeps(st, signer, dm)
	before := monitoring.P2PWalletIngressDedupeSkipCount()
	deps.Msg = wire
	DispatchInboundP2P(deps)
	if len(st.stored) != 1 {
		t.Fatalf("after mesh: want 1 store, got %d", len(st.stored))
	}
	deps.Msg = walletMsg
	DispatchInboundP2P(deps)
	if len(st.stored) != 1 {
		t.Fatalf("after wallet duplicate: want 1 store, got %d", len(st.stored))
	}
	if monitoring.P2PWalletIngressDedupeSkipCount()-before != 1 {
		t.Fatalf("dedupe skip: before=%d after=%d", before, monitoring.P2PWalletIngressDedupeSkipCount())
	}
}

func TestDispatchInboundP2P_dedupeWalletThenMeshWire(t *testing.T) {
	txTestDedupeReset(t)
	signer := newTestPoE(t)
	st := &sliceStorage{}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	walletMsg := buildP2PTestTxMessageWithGeo(t, signer, "US")
	wire := meshCompanionOf(t, walletMsg, "route-a")
	deps := meshDeps(st, signer, dm)
	before := monitoring.P2PWalletIngressDedupeSkipCount()
	deps.Msg = walletMsg
	DispatchInboundP2P(deps)
	deps.Msg = wire
	DispatchInboundP2P(deps)
	if len(st.stored) != 1 {
		t.Fatalf("want 1 store, got %d", len(st.stored))
	}
	if monitoring.P2PWalletIngressDedupeSkipCount()-before != 1 {
		t.Fatalf("dedupe skip: before=%d after=%d", before, monitoring.P2PWalletIngressDedupeSkipCount())
	}
}
