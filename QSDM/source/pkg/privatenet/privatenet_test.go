package privatenet

import "testing"

func TestIsPrivate(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":         true,
		"::1":               true,
		"10.1.2.3":          true,
		"172.16.0.1":        true,
		"172.31.255.254":    true,
		"192.168.1.10":      true,
		"100.64.0.1":        true,
		"100.101.102.103":   true,
		"100.127.255.254":   true,
		"fd7a:115c:a1e0::1": true,
		"fc00::1":           true,
		"0.0.0.0":           false,
		"::":                false,
		"8.8.8.8":           false,
		"100.63.255.255":    false,
		"100.128.0.1":       false,
		"172.32.0.1":        false,
		"169.254.1.1":       false,
		"fe80::1":           false,
		"224.0.0.1":         false,
		"2001:db8::1":       false,
	}
	for s, want := range cases {
		if got := IsPrivate(ParseIP(s)); got != want {
			t.Errorf("IsPrivate(%s) = %v, want %v", s, got, want)
		}
	}
	if IsPrivate(nil) {
		t.Fatal("nil must not be private")
	}
}

func TestParseIP(t *testing.T) {
	if ip := ParseIP(" localhost "); ip == nil || !ip.IsLoopback() {
		t.Fatalf("localhost: %v", ip)
	}
	if ip := ParseIP("[fd7a:115c:a1e0::5]"); ip == nil || !IsPrivate(ip) {
		t.Fatalf("bracketed v6: %v", ip)
	}
	if ParseIP("vps.example") != nil || ParseIP("") != nil {
		t.Fatal("names and empty strings are not literal addresses")
	}
}
