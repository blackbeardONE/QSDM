package contracts

import (
	"encoding/json"
	"testing"
)

func transferABI() *ABI {
	return &ABI{
		Functions: []Function{
			{
				Name: "transfer",
				Inputs: []Param{
					{Name: "to", Type: "address"},
					{Name: "amount", Type: "uint64"},
				},
				Outputs: []Param{
					{Name: "success", Type: "bool"},
					{Name: "new_balance", Type: "uint64"},
				},
				StateMutating: true,
			},
			{
				Name: "balanceOf",
				Inputs: []Param{
					{Name: "address", Type: "string"},
				},
				Outputs: []Param{
					{Name: "balance", Type: "uint64"},
				},
			},
		},
	}
}

func TestABICodec_EncodeCall(t *testing.T) {
	codec := NewABICodec()
	abi := transferABI()

	encoded, err := codec.EncodeCall(abi, "transfer", map[string]interface{}{
		"to":     "0xBob",
		"amount": float64(100),
	})
	if err != nil {
		t.Fatalf("EncodeCall: %v", err)
	}
	if encoded["to"] != "0xBob" {
		t.Fatalf("expected 0xBob, got %v", encoded["to"])
	}
	if encoded["amount"] != uint64(100) {
		t.Fatalf("expected uint64(100), got %v (%T)", encoded["amount"], encoded["amount"])
	}
}

func TestABICodec_EncodeCall_MissingParam(t *testing.T) {
	codec := NewABICodec()
	_, err := codec.EncodeCall(transferABI(), "transfer", map[string]interface{}{
		"to": "bob",
	})
	if err == nil {
		t.Fatal("expected error for missing 'amount' param")
	}
}

func TestABICodec_EncodeCall_UnknownFunction(t *testing.T) {
	codec := NewABICodec()
	_, err := codec.EncodeCall(transferABI(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown function")
	}
}

func TestABICodec_EncodeCall_StringAmount(t *testing.T) {
	codec := NewABICodec()
	encoded, err := codec.EncodeCall(transferABI(), "transfer", map[string]interface{}{
		"to":     "bob",
		"amount": "42",
	})
	if err != nil {
		t.Fatalf("EncodeCall with string amount: %v", err)
	}
	if encoded["amount"] != uint64(42) {
		t.Fatalf("expected 42, got %v", encoded["amount"])
	}
}

func TestABICodec_EncodeCall_InvalidType(t *testing.T) {
	codec := NewABICodec()
	_, err := codec.EncodeCall(transferABI(), "transfer", map[string]interface{}{
		"to":     "bob",
		"amount": "not_a_number",
	})
	if err == nil {
		t.Fatal("expected error for invalid uint64 value")
	}
}

func TestABICodec_DecodeOutput(t *testing.T) {
	codec := NewABICodec()
	decoded, err := codec.DecodeOutput(transferABI(), "transfer", map[string]interface{}{
		"success":     true,
		"new_balance": float64(950),
		"extra":       "kept",
	})
	if err != nil {
		t.Fatalf("DecodeOutput: %v", err)
	}
	if decoded["success"] != true {
		t.Fatal("expected success=true")
	}
	if decoded["new_balance"] != uint64(950) {
		t.Fatalf("expected 950, got %v", decoded["new_balance"])
	}
	if decoded["extra"] != "kept" {
		t.Fatal("extra fields should be carried through")
	}
}

func TestABICodec_ValidateABI(t *testing.T) {
	codec := NewABICodec()

	issues := codec.ValidateABI(transferABI())
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", issues)
	}
}

func TestABICodec_ValidateABI_Issues(t *testing.T) {
	codec := NewABICodec()

	bad := &ABI{
		Functions: []Function{
			{Name: ""},
			{Name: "dup"},
			{Name: "dup"},
			{Name: "typed", Inputs: []Param{{Name: "x", Type: "unknown_type"}}},
		},
	}
	issues := codec.ValidateABI(bad)
	if len(issues) < 3 {
		t.Fatalf("expected at least 3 issues, got %d: %v", len(issues), issues)
	}
}

func TestABICodec_ValidateNil(t *testing.T) {
	codec := NewABICodec()
	issues := codec.ValidateABI(nil)
	if len(issues) != 1 {
		t.Fatal("expected 1 issue for nil ABI")
	}
}

func TestABICodec_NilABI_Passthrough(t *testing.T) {
	codec := NewABICodec()
	args := map[string]interface{}{"x": 1}
	encoded, err := codec.EncodeCall(nil, "any", args)
	if err != nil {
		t.Fatal("nil ABI should pass through")
	}
	if encoded["x"] != 1 {
		t.Fatal("args should be passed through")
	}
}

func TestABICodec_CoerceBool(t *testing.T) {
	codec := NewABICodec()
	abi := &ABI{Functions: []Function{{Name: "f", Inputs: []Param{{Name: "flag", Type: "bool"}}}}}

	cases := []struct {
		input    interface{}
		expected bool
	}{
		{true, true},
		{false, false},
		{"true", true},
		{"false", false},
		{float64(1), true},
		{float64(0), false},
	}

	for _, tc := range cases {
		encoded, err := codec.EncodeCall(abi, "f", map[string]interface{}{"flag": tc.input})
		if err != nil {
			t.Fatalf("EncodeCall bool(%v): %v", tc.input, err)
		}
		if encoded["flag"] != tc.expected {
			t.Fatalf("expected %v for input %v, got %v", tc.expected, tc.input, encoded["flag"])
		}
	}
}

func TestABIToJSON_Roundtrip(t *testing.T) {
	original := transferABI()
	data, err := ABIToJSON(original)
	if err != nil {
		t.Fatalf("ABIToJSON: %v", err)
	}

	parsed, err := ABIFromJSON(data)
	if err != nil {
		t.Fatalf("ABIFromJSON: %v", err)
	}

	if len(parsed.Functions) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(parsed.Functions))
	}
	if parsed.Functions[0].Name != "transfer" {
		t.Fatalf("expected transfer, got %s", parsed.Functions[0].Name)
	}
}

func TestABIFromJSON_Invalid(t *testing.T) {
	_, err := ABIFromJSON([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestABICodec_JSONNumberCoercion(t *testing.T) {
	codec := NewABICodec()
	abi := transferABI()

	// Simulate JSON-decoded number
	raw := map[string]interface{}{
		"to":     "bob",
		"amount": json.Number("999"),
	}
	encoded, err := codec.EncodeCall(abi, "transfer", raw)
	if err != nil {
		t.Fatalf("EncodeCall with json.Number: %v", err)
	}
	if encoded["amount"] != uint64(999) {
		t.Fatalf("expected 999, got %v", encoded["amount"])
	}
}
