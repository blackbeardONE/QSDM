package monitoring

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNvidiaLockP2PGate_nilAllows(t *testing.T) {
	var g *NvidiaLockP2PGate
	if !g.Allows() {
		t.Fatal("nil gate should allow")
	}
}

func TestNvidiaLockP2PGate_disabledAllows(t *testing.T) {
	g := &NvidiaLockP2PGate{Enabled: false, MaxProofAge: time.Hour}
	ResetNGCProofsForTest()
	if !g.Allows() {
		t.Fatal("disabled gate should allow")
	}
}

func TestNvidiaLockP2PGate_enabledNoProofBlocks(t *testing.T) {
	ResetNGCProofsForTest()
	g := &NvidiaLockP2PGate{Enabled: true, MaxProofAge: time.Hour}
	if g.Allows() {
		t.Fatal("expected block with empty proof ring")
	}
}

func TestNvidiaLockP2PGate_enabledWithGPUProofAllows(t *testing.T) {
	ResetNGCProofsForTest()
	payload := map[string]interface{}{
		"architecture":    "NVIDIA-Locked QSDM test",
		"cuda_proof_hash": "cafe",
		"gpu_fingerprint": map[string]interface{}{"available": true},
	}
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	g := &NvidiaLockP2PGate{Enabled: true, MaxProofAge: time.Hour}
	if !g.Allows() {
		t.Fatal("expected allow with qualifying proof")
	}
}

func TestNvidiaLockP2PGate_nonConsuming(t *testing.T) {
	ResetNGCProofsForTest()
	payload := map[string]interface{}{
		"architecture":    "NVIDIA-Locked QSDM test",
		"cuda_proof_hash": "beef",
		"gpu_fingerprint": map[string]interface{}{"available": true},
	}
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	g := &NvidiaLockP2PGate{Enabled: true, MaxProofAge: time.Hour}
	if !g.Allows() {
		t.Fatal("first Allows should pass")
	}
	if !g.Allows() {
		t.Fatal("second Allows should still pass (non-consuming)")
	}
}
