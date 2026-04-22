package submesh

import (
	"errors"
	"strings"
	"testing"
)

func TestEnforceWalletSendPolicy_noSubmeshes(t *testing.T) {
	m := NewDynamicSubmeshManager()
	if err := m.EnforceWalletSendPolicy(1, "US", []byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestEnforceWalletSendPolicy_sentinelErrors(t *testing.T) {
	m := NewDynamicSubmeshManager()
	m.AddOrUpdateSubmesh(&DynamicSubmesh{Name: "a", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 2})
	err := m.EnforceWalletSendPolicy(0.01, "XX", []byte("x"))
	if !errors.Is(err, ErrSubmeshNoRoute) {
		t.Fatalf("want ErrSubmeshNoRoute, got %v", err)
	}
	err = m.EnforceWalletSendPolicy(0.01, "US", []byte("abc"))
	if !errors.Is(err, ErrSubmeshPayloadTooLarge) {
		t.Fatalf("want ErrSubmeshPayloadTooLarge, got %v", err)
	}
}

func TestMatchP2POrReject(t *testing.T) {
	m := NewDynamicSubmeshManager()
	ds, err := m.MatchP2POrReject(0.1, "US", []byte("hi"))
	if err != nil || ds != nil {
		t.Fatalf("empty manager: ds=%v err=%v", ds, err)
	}
	m.AddOrUpdateSubmesh(&DynamicSubmesh{Name: "a", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 5})
	ds, err = m.MatchP2POrReject(0.01, "US", []byte("12345"))
	if err != nil || ds == nil || ds.Name != "a" {
		t.Fatalf("match: ds=%v err=%v", ds, err)
	}
	if _, err = m.MatchP2POrReject(0.01, "US", []byte("123456")); err == nil {
		t.Fatal("expected size reject")
	}
	if _, err = m.MatchP2POrReject(0.01, "XX", []byte("x")); err == nil {
		t.Fatal("expected route reject")
	}
}

func TestEnforceWalletSendPolicy_matchAndSize(t *testing.T) {
	m := NewDynamicSubmeshManager()
	m.AddOrUpdateSubmesh(&DynamicSubmesh{
		Name: "a", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 10,
	})
	if err := m.EnforceWalletSendPolicy(0.01, "US", []byte("1234567890")); err != nil {
		t.Fatal(err)
	}
	if err := m.EnforceWalletSendPolicy(0.01, "US", []byte("12345678901")); err == nil {
		t.Fatal("expected size error")
	}
	if err := m.EnforceWalletSendPolicy(0.01, "XX", []byte("tiny")); err == nil {
		t.Fatal("expected route error")
	}
}

func TestEnforcePrivilegedLedgerPayloadCap(t *testing.T) {
	m := NewDynamicSubmeshManager()
	m.AddOrUpdateSubmesh(&DynamicSubmesh{Name: "a", FeeThreshold: 0, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 100})
	m.AddOrUpdateSubmesh(&DynamicSubmesh{Name: "b", FeeThreshold: 0, PriorityLevel: 1, GeoTags: []string{"EU"}, MaxPayloadBytes: 50})
	if err := m.EnforcePrivilegedLedgerPayloadCap([]byte(strings.Repeat("x", 50))); err != nil {
		t.Fatal(err)
	}
	if err := m.EnforcePrivilegedLedgerPayloadCap([]byte(strings.Repeat("x", 51))); err == nil {
		t.Fatal("expected cap error")
	}
}
