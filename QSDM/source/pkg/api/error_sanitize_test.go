package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSanitizeForLog_StripsCRLF(t *testing.T) {
	in := "alice\r\nADMIN LOGIN SUCCESS"
	out := SanitizeForLog(in)
	if strings.ContainsAny(out, "\r\n") {
		t.Fatalf("expected CR/LF stripped, got %q", out)
	}
}

func TestSanitizeForLog_StripsANSIAndControl(t *testing.T) {
	in := "alice\x1b[31mERROR\x1b[0m\x00\x07"
	out := SanitizeForLog(in)
	for _, ch := range []byte{0x1b, 0x00, 0x07} {
		if strings.IndexByte(out, ch) >= 0 {
			t.Fatalf("control byte %#x leaked through: %q", ch, out)
		}
	}
}

func TestSanitizeForLog_TruncatesLong(t *testing.T) {
	in := strings.Repeat("x", MaxStringLength+200)
	out := SanitizeForLog(in)
	if !strings.HasSuffix(out, "...(truncated)") {
		t.Fatalf("expected truncation marker, got len=%d tail=%q", len(out), out[len(out)-32:])
	}
}

func TestSanitizeForLogN_PreservesPrintable(t *testing.T) {
	in := "hello world 123"
	if out := SanitizeForLogN(in, 32); out != in {
		t.Fatalf("expected passthrough, got %q", out)
	}
}

func TestWriteServerError_ProductionMaskedDevVerbose(t *testing.T) {
	prev := IsProductionMode()
	defer SetProductionMode(prev)

	// Dev mode: error text included.
	SetProductionMode(false)
	w := httptest.NewRecorder()
	WriteServerError(w, nil, "op", errors.New("internal DB row missing"))
	if !strings.Contains(w.Body.String(), "internal DB row missing") {
		t.Fatalf("dev mode expected raw error in body, got %s", w.Body.String())
	}

	// Production mode: error text hidden, correlation id present.
	SetProductionMode(true)
	w2 := httptest.NewRecorder()
	WriteServerError(w2, nil, "op", errors.New("internal DB row missing"))
	if strings.Contains(w2.Body.String(), "internal DB row missing") {
		t.Fatalf("production mode must NOT echo raw error: %s", w2.Body.String())
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v body=%s", err, w2.Body.String())
	}
	if !strings.Contains(body.Message, "id=") {
		t.Fatalf("expected correlation id in message, got %q", body.Message)
	}
}
