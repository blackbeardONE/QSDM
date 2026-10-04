package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

func TestSubmitSignedRejectsUnspendableRecipientShape(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Fatal(err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	h.storage.(*mockStorage).balances[ws.GetAddress()] = 5
	for name, recipient := range map[string]string{
		"short_hex":      strings.Repeat("a", 32),
		"long_hex":       strings.Repeat("a", 128),
		"uppercase_hex":  strings.Repeat("A", 64),
		"mixed_case_hex": strings.Repeat("aB", 32),
	} {
		t.Run(name, func(t *testing.T) {
			// A genuine valid signature must not make an unspendable recipient valid.
			envelope := buildSignedEnvelopeWithNonce(t, ws, recipient, 1, 0.01, []string{strings.Repeat("a", 32), strings.Repeat("b", 32)}, 1)
			response := postSubmitSigned(t, h, envelope)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d; want400", response.Code)
			}
			if h.storage.(*mockStorage).balances[ws.GetAddress()] != 5 {
				t.Fatal("rejection changed sender balance")
			}
			if _, exists := h.storage.(*mockStorage).balances[recipient]; exists {
				t.Fatal("rejection created recipient account")
			}
		})
	}
}
