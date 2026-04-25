package monitoring

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestNvidiaLockProofOK_empty(t *testing.T) {
	ResetNGCProofsForTest()

	ok, msg := NvidiaLockProofOK(time.Minute, "", "", false)
	if ok {
		t.Fatal("expected false")
	}
	if msg == "" {
		t.Fatal("expected detail message")
	}
}

func TestNvidiaLockProofOK_cpuFingerprintRejected(t *testing.T) {
	ResetNGCProofsForTest()

	payload := map[string]interface{}{
		"architecture":    "NVIDIA-Locked QSDM test",
		"cuda_proof_hash": "abc",
		"gpu_fingerprint": map[string]interface{}{"available": false, "error": "no gpu"},
	}
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	ok, _ := NvidiaLockProofOK(time.Hour, "", "", false)
	if ok {
		t.Fatal("CPU-only fingerprint should not satisfy lock")
	}
}

func TestNvidiaLockProofOK_gpuAccepted(t *testing.T) {
	ResetNGCProofsForTest()

	payload := map[string]interface{}{
		"architecture":    "NVIDIA-Locked QSDM test",
		"cuda_proof_hash": "deadbeef",
		"gpu_fingerprint": map[string]interface{}{
			"available": true,
			"devices": []interface{}{
				map[string]interface{}{"name": "Test GPU", "index": "0"},
			},
		},
	}
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	ok, detail := NvidiaLockProofOK(time.Hour, "", "", false)
	if !ok {
		t.Fatalf("expected true, got %q", detail)
	}
}

func TestNvidiaLockProofOK_expectedNodeIDMismatch(t *testing.T) {
	ResetNGCProofsForTest()
	payload := map[string]interface{}{
		"architecture":    "NVIDIA-Locked QSDM test",
		"cuda_proof_hash": "x",
		"qsdm_node_id": "a",
		"gpu_fingerprint": map[string]interface{}{"available": true},
	}
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	ok, _ := NvidiaLockProofOK(time.Hour, "b", "", false)
	if ok {
		t.Fatal("expected mismatch on qsdm_node_id")
	}
}

func TestNvidiaLockProofOK_expectedNodeIDMatch(t *testing.T) {
	ResetNGCProofsForTest()
	payload := map[string]interface{}{
		"architecture":     "NVIDIA-Locked QSDM test",
		"cuda_proof_hash":  "y",
		"qsdm_node_id": "validator-7",
		"gpu_fingerprint": map[string]interface{}{
			"available": true,
			"devices":   []interface{}{map[string]interface{}{"name": "G", "index": "0"}},
		},
	}
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	ok, detail := NvidiaLockProofOK(time.Hour, "validator-7", "", false)
	if !ok {
		t.Fatalf("expected true, got %q", detail)
	}
}

func TestNvidiaLockProofOK_hmacRequiredInvalid(t *testing.T) {
	ResetNGCProofsForTest()
	secret := "Charming123"
	payload := map[string]interface{}{
		"architecture":     "NVIDIA-Locked QSDM test",
		"cuda_proof_hash":  "c1",
		"timestamp_utc":    "2026-04-01T12:00:00+00:00",
		"qsdm_node_id": "",
		"gpu_fingerprint":  map[string]interface{}{"available": true},
		"qsdm_proof_hmac": "deadbeef",
	}
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	ok, _ := NvidiaLockProofOK(time.Hour, "", secret, false)
	if ok {
		t.Fatal("expected bad hex HMAC to fail")
	}
}

func TestNvidiaLockProofOK_hmacRequiredValid(t *testing.T) {
	ResetNGCProofsForTest()
	secret := "Charming123"
	payload := map[string]interface{}{
		"architecture":     "NVIDIA-Locked QSDM test",
		"cuda_proof_hash":  "c2",
		"timestamp_utc":    "2026-04-01T12:00:00+00:00",
		"qsdm_node_id": "n1",
		"gpu_fingerprint":  map[string]interface{}{"available": true},
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(NGCProofHMACPayload(payload)))
	payload["qsdm_proof_hmac"] = hex.EncodeToString(mac.Sum(nil))
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	ok, detail := NvidiaLockProofOK(time.Hour, "n1", secret, false)
	if !ok {
		t.Fatalf("expected true, got %q", detail)
	}
}

func TestNvidiaLockProofOK_consumeRemovesProof(t *testing.T) {
	ResetNGCProofsForTest()
	payload := map[string]interface{}{
		"architecture":    "NVIDIA-Locked QSDM test",
		"cuda_proof_hash": "consume-me",
		"gpu_fingerprint": map[string]interface{}{"available": true},
	}
	raw, _ := json.Marshal(payload)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}
	ok, _ := NvidiaLockProofOK(time.Hour, "", "", true)
	if !ok {
		t.Fatal("expected first check ok")
	}
	ok2, _ := NvidiaLockProofOK(time.Hour, "", "", false)
	if ok2 {
		t.Fatal("proof should have been consumed")
	}
}
