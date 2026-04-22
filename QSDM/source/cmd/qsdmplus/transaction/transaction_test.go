package transaction

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
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

func buildP2PTestTxMessage(t *testing.T, poe *consensus.ProofOfEntanglement) []byte {
	return buildP2PTestTxMessageWithGeo(t, poe, "")
}

func buildP2PTestTxMessageWithGeo(t *testing.T, poe *consensus.ProofOfEntanglement, geoTag string) []byte {
	t.Helper()
	const id32 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	parent1 := strings.Repeat("a", 32)
	parent2 := strings.Repeat("b", 32)
	sender := strings.Repeat("1", 32)
	recipient := strings.Repeat("2", 32)

	withoutSig := Transaction{
		ID:          id32,
		Sender:      sender,
		Recipient:   recipient,
		Amount:      1.0,
		Fee:         0.1,
		GeoTag:      geoTag,
		ParentCells: []string{parent1, parent2},
		Signature:   "",
		Timestamp:   "2010-01-02T15:04:05Z",
	}
	body, err := json.Marshal(withoutSig)
	if err != nil {
		t.Fatal(err)
	}

	var sigHex string
	if poe != nil {
		sig, err := poe.Sign(body)
		if err != nil {
			t.Fatalf("sign tx: %v", err)
		}
		sigHex = hex.EncodeToString(sig)
	} else {
		sigHex = strings.Repeat("cd", 50)
	}

	full := withoutSig
	full.Signature = sigHex
	if poe != nil {
		full.PublicKey = poe.MLDSAPublicKeyHex()
	}
	out, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHandleTransaction_P2PGate_blocksWithoutProof(t *testing.T) {
	txTestDedupeReset(t)
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	poe := consensus.NewProofOfEntanglement()
	if poe == nil {
		t.Log("ProofOfEntanglement nil (no CGO): stub validation still exercises P2P gate after parse")
	}

	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	st := &sliceStorage{}
	gate := &monitoring.NvidiaLockP2PGate{Enabled: true, MaxProofAge: 24 * time.Hour}

	before := monitoring.NvidiaLockP2PRejectCount()
	msg := buildP2PTestTxMessage(t, poe)
	HandleTransaction(logger, msg, dm, nil, poe, st, gate)

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

	poe := consensus.NewProofOfEntanglement()
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	st := &sliceStorage{}
	gate := &monitoring.NvidiaLockP2PGate{Enabled: true, MaxProofAge: 24 * time.Hour}

	msg := buildP2PTestTxMessage(t, poe)
	before := monitoring.NvidiaLockP2PRejectCount()
	HandleTransaction(logger, msg, dm, nil, poe, st, gate)

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

	poe := consensus.NewProofOfEntanglement()
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	st := &sliceStorage{}
	msg := buildP2PTestTxMessage(t, poe)
	HandleTransaction(logger, msg, dm, nil, poe, st, nil)
	if len(st.stored) != 1 {
		t.Fatalf("expected store, got %d", len(st.stored))
	}
}

func TestHandleTransaction_SubmeshConfigured_rejectsWhenNoRoute(t *testing.T) {
	txTestDedupeReset(t)
	poe := consensus.NewProofOfEntanglement()
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	st := &sliceStorage{}
	msg := buildP2PTestTxMessage(t, poe)
	HandleTransaction(logger, msg, dm, nil, poe, st, nil)
	if len(st.stored) != 0 {
		t.Fatalf("expected no store when geotag does not match submesh, got %d", len(st.stored))
	}
}

func TestHandleTransaction_SubmeshConfigured_storesWhenRouteMatches(t *testing.T) {
	txTestDedupeReset(t)
	poe := consensus.NewProofOfEntanglement()
	if poe == nil {
		t.Skip("CGO / ProofOfEntanglement required for signed tx storage path")
	}
	logger := logging.NewSilentLogger()
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	st := &sliceStorage{}
	msg := buildP2PTestTxMessageWithGeo(t, poe, "US")
	HandleTransaction(logger, msg, dm, nil, poe, st, nil)
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

func TestDispatchInboundP2P_mesh3DWire(t *testing.T) {
	txTestDedupeReset(t)
	logger := logging.NewSilentLogger()
	st := &sliceStorage{}
	txData := bytes.Repeat([]byte("d"), 64)
	tx := &mesh3d.Transaction{
		ID: string(bytes.Repeat([]byte("y"), 32)),
		ParentCells: []mesh3d.ParentCell{
			{ID: "a1", Data: bytes.Repeat([]byte{1}, 32)},
			{ID: "a2", Data: bytes.Repeat([]byte{2}, 32)},
			{ID: "a3", Data: bytes.Repeat([]byte{3}, 32)},
		},
		Data: txData,
	}
	msg, err := EncodeMesh3DWire(tx, "route-a")
	if err != nil {
		t.Fatal(err)
	}
	DispatchInboundP2P(DispatchDeps{
		Logger:            logger,
		Msg:               msg,
		DynamicManager:    submesh.NewDynamicSubmeshManager(),
		WasmSdk:           nil,
		Consensus:         consensus.NewProofOfEntanglement(),
		Storage:           st,
		NvidiaGate:        nil,
		Mesh3dValidator:   mesh3d.NewMesh3DValidator(),
		QuarantineManager: quarantine.NewQuarantineManager(0.5),
		ReputationManager: quarantine.NewReputationManager(10, 5),
	})
	if len(st.stored) != 1 || !bytes.Equal(st.stored[0], txData) {
		t.Fatalf("expected mesh payload stored, got %d stored", len(st.stored))
	}
}

func TestDispatchInboundP2P_walletJSONNotMesh(t *testing.T) {
	txTestDedupeReset(t)
	poe := consensus.NewProofOfEntanglement()
	if poe == nil {
		t.Skip("CGO / ProofOfEntanglement required")
	}
	logger := logging.NewSilentLogger()
	st := &sliceStorage{}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	msg := buildP2PTestTxMessageWithGeo(t, poe, "US")
	DispatchInboundP2P(DispatchDeps{
		Logger:            logger,
		Msg:               msg,
		DynamicManager:    dm,
		WasmSdk:           nil,
		Consensus:         poe,
		Storage:           st,
		NvidiaGate:        nil,
		Mesh3dValidator:   mesh3d.NewMesh3DValidator(),
		QuarantineManager: quarantine.NewQuarantineManager(0.5),
		ReputationManager: quarantine.NewReputationManager(10, 5),
	})
	if len(st.stored) != 1 {
		t.Fatalf("expected one wallet tx stored, got %d", len(st.stored))
	}
}

func TestDispatchInboundP2P_dedupeMeshThenWalletJSON(t *testing.T) {
	txTestDedupeReset(t)
	poe := consensus.NewProofOfEntanglement()
	if poe == nil {
		t.Skip("CGO / ProofOfEntanglement required")
	}
	logger := logging.NewSilentLogger()
	st := &sliceStorage{}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	walletMsg := buildP2PTestTxMessageWithGeo(t, poe, "US")
	var inner struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(walletMsg, &inner); err != nil {
		t.Fatal(err)
	}
	meshTx := &mesh3d.Transaction{
		ID: inner.ID,
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: bytes.Repeat([]byte{1}, 32)},
			{ID: "p2", Data: bytes.Repeat([]byte{2}, 32)},
			{ID: "p3", Data: bytes.Repeat([]byte{3}, 32)},
		},
		Data: walletMsg,
	}
	wire, err := EncodeMesh3DWire(meshTx, "route-a")
	if err != nil {
		t.Fatal(err)
	}
	deps := DispatchDeps{
		Logger:            logger,
		DynamicManager:    dm,
		WasmSdk:           nil,
		Consensus:         poe,
		Storage:           st,
		NvidiaGate:        nil,
		Mesh3dValidator:   mesh3d.NewMesh3DValidator(),
		QuarantineManager: quarantine.NewQuarantineManager(0.5),
		ReputationManager: quarantine.NewReputationManager(10, 5),
	}
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
	poe := consensus.NewProofOfEntanglement()
	if poe == nil {
		t.Skip("CGO / ProofOfEntanglement required")
	}
	logger := logging.NewSilentLogger()
	st := &sliceStorage{}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "mp", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"},
	})
	walletMsg := buildP2PTestTxMessageWithGeo(t, poe, "US")
	var inner struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(walletMsg, &inner); err != nil {
		t.Fatal(err)
	}
	meshTx := &mesh3d.Transaction{
		ID: inner.ID,
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: bytes.Repeat([]byte{1}, 32)},
			{ID: "p2", Data: bytes.Repeat([]byte{2}, 32)},
			{ID: "p3", Data: bytes.Repeat([]byte{3}, 32)},
		},
		Data: walletMsg,
	}
	wire, err := EncodeMesh3DWire(meshTx, "route-a")
	if err != nil {
		t.Fatal(err)
	}
	deps := DispatchDeps{
		Logger:            logger,
		DynamicManager:    dm,
		WasmSdk:           nil,
		Consensus:         poe,
		Storage:           st,
		NvidiaGate:        nil,
		Mesh3dValidator:   mesh3d.NewMesh3DValidator(),
		QuarantineManager: quarantine.NewQuarantineManager(0.5),
		ReputationManager: quarantine.NewReputationManager(10, 5),
	}
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
