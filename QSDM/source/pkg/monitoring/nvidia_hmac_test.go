package monitoring

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestNGCProofHMACPayload_v1v2(t *testing.T) {
	m1 := map[string]interface{}{
		"qsdmplus_node_id": "a",
		"cuda_proof_hash":  "b",
		"timestamp_utc":    "c",
	}
	if got := NGCProofHMACPayload(m1); got != "v1\na\nb\nc\n" {
		t.Fatalf("v1: %q", got)
	}
	m2 := map[string]interface{}{
		"qsdmplus_node_id":     "a",
		"cuda_proof_hash":      "b",
		"timestamp_utc":        "c",
		"qsdmplus_ingest_nonce": "n1",
	}
	if got := NGCProofHMACPayload(m2); got != "v2\na\nb\nc\nn1\n" {
		t.Fatalf("v2: %q", got)
	}
}

func TestNGCProofHMACValid_v2(t *testing.T) {
	secret := "Charming123"
	m := map[string]interface{}{
		"qsdmplus_node_id":     "",
		"cuda_proof_hash":      "x",
		"timestamp_utc":        "y",
		"qsdmplus_ingest_nonce": "z",
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(NGCProofHMACPayload(m)))
	m["qsdmplus_proof_hmac"] = hex.EncodeToString(mac.Sum(nil))
	if !NGCProofHMACValid(m, secret) {
		t.Fatal("expected valid v2 HMAC")
	}
}
