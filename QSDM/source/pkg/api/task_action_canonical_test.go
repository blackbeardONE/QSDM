package api

import (
	"encoding/json"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// The consensus-side verifier reproduces the signing envelope rather than
// importing it, because pkg/api depends on pkg/chain and not the reverse. Two
// definitions of one wire format drift, and the failure mode here is not
// cosmetic: if chain's canonical bytes differ from what the client actually
// signed, every legitimately signed task action is rejected at apply time.
//
// This lives in pkg/api because that is the side that owns the authoritative
// form -- the bytes clients sign today.
func TestCanonicalTaskActionBytesMatchTheSignedEnvelope(t *testing.T) {
	cases := []QSDMTaskActionEnvelope{
		{ID: "a1", Sender: "s1", TaskID: "t1", Action: "stake", Amount: 5, Nonce: 3, Timestamp: "2026-08-14T00:00:00Z"},
		// Exercise every omitempty field, in both states.
		{ID: "a2", Sender: "s2", TaskID: "t2", Action: "start", Timestamp: "2026-08-14T00:00:01Z"},
		{ID: "a3", Sender: "s3", TaskID: "t3", Action: "submit", Payload: "p", Nonce: 9, Timestamp: "x"},
		{ID: "a4", Sender: "s4", TaskID: "t4", Action: "fund", Amount: 0.125, Payload: "q", Timestamp: "y"},
	}
	for _, env := range cases {
		// Exactly what verifyQSDMTaskActionEnvelope canonicalises.
		unsigned := env
		unsigned.Signature = ""
		unsigned.PublicKey = ""
		apiBytes, err := json.Marshal(unsigned)
		if err != nil {
			t.Fatalf("api canonicalise: %v", err)
		}

		chainBytes, err := chain.CanonicalTaskActionSigningBytes(qsdmTaskActionToChain(env))
		if err != nil {
			t.Fatalf("chain canonicalise: %v", err)
		}

		if string(apiBytes) != string(chainBytes) {
			t.Errorf("canonical forms differ for %q:\n  api   %s\n  chain %s",
				env.Action, apiBytes, chainBytes)
		}
	}
}
