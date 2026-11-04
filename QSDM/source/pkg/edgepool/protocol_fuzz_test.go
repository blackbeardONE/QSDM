package edgepool

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Fuzz targets for the edge-pool credentials and envelopes a Relay or Agent
// accepts from the network: the federation and Mother Hive contexts carried
// in request headers, request and job HMACs, and the identifier validators.

// fuzzIDChars is the identifier rule the edge-pool patterns spell as regular
// expressions, written out: min..max bytes, the first from [A-Za-z0-9], the
// rest from [A-Za-z0-9] or extra.
func fuzzIDChars(s string, min, max int, extra string) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || !strings.ContainsRune(extra, rune(c))) {
			return false
		}
	}
	return true
}

func FuzzDecodeFederationContext(f *testing.F) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	encode := func(v FederationContext) string {
		encoded, _, err := EncodeFederationContext(v, now)
		if err != nil {
			f.Fatal(err)
		}
		return encoded
	}
	valid := FederationContext{
		Version: FederationContextVersion, RelayURL: "https://relay.example.test", OfferID: "offer-test-1",
		ProviderName: "Test Relay", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
		WorkloadIDs: []string{WorkloadRAMMemoryScan, WorkloadCPUHashChain},
	}
	full := valid
	full.RelayURL = "https://relay.example.test:8443/ignored/path"
	full.OfferID = "Offer:2026.08_a-b"
	full.ProviderWallet = strings.Repeat("a1", 32)
	full.ConsumerWallet = strings.Repeat("Z", 128)
	full.ExpiresAt = now.Add(MaximumFederationInvitationLifetime).Format(time.RFC3339)
	full.WorkloadIDs = DefaultFederationWorkloadIDs()
	f.Add(encode(valid))
	f.Add(encode(full))
	canonical, _ := json.Marshal(valid)
	raw := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	f.Add(raw(string(canonical) + " "))
	f.Add(raw(strings.Replace(string(canonical), `"version":1`, `"version":1,"extra":true`, 1)))
	f.Add(raw(strings.Replace(string(canonical), `"relay_url":"https://relay.example.test"`, `"relay_url":"https://relay.example.test/"`, 1)))
	f.Add(raw(strings.Replace(string(canonical), `https:`, `http:`, 1)))
	f.Add(raw(string(canonical) + "{}"))
	f.Add(base64.URLEncoding.EncodeToString(canonical))
	f.Add(raw(`{"version":1,"relay_url":"https://u:p@h/","offer_id":"abc","provider_name":"x","expires_at":"2026-08-01T13:00:00Z","workload_ids":["qsdm.cpu.hash-chain.v1","qsdm.cpu.hash-chain.v1"]}`))
	f.Add("")
	f.Add(strings.Repeat("A", 4097))

	master := bytes.Repeat([]byte{0x61}, 32)
	f.Fuzz(func(t *testing.T, encoded string) {
		got, err := DecodeFederationContext(encoded, now)
		if err != nil {
			return
		}
		// Canonical: the accepted encoding is the only encoding of its value.
		again, normalized, err := EncodeFederationContext(got, now)
		if err != nil || again != encoded || !reflect.DeepEqual(normalized, got) {
			t.Fatalf("accepted %q but it re-encodes as %q (%v)", encoded, again, err)
		}
		u, err := url.Parse(got.RelayURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" ||
			u.Fragment != "" || u.Path != "/" {
			t.Fatalf("accepted Relay URL %q", got.RelayURL)
		}
		if got.Version != FederationContextVersion || !fuzzIDChars(got.OfferID, 3, 96, "._:-") ||
			got.ProviderName == "" || got.ProviderName != strings.TrimSpace(got.ProviderName) || len(got.ProviderName) > 80 {
			t.Fatalf("accepted version, offer id or provider name: %+v", got)
		}
		for _, wallet := range []string{got.ProviderWallet, got.ConsumerWallet} {
			if wallet != "" && !fuzzIDChars(wallet, 32, 128, "") {
				t.Fatalf("accepted wallet %q", wallet)
			}
		}
		expires, err := time.Parse(time.RFC3339, got.ExpiresAt)
		if err != nil || got.ExpiresAt != expires.UTC().Format(time.RFC3339) || !expires.After(now) ||
			expires.After(now.Add(MaximumFederationInvitationLifetime)) {
			t.Fatalf("accepted expiry %q at %s", got.ExpiresAt, now)
		}
		if len(got.WorkloadIDs) == 0 || !sort.StringsAreSorted(got.WorkloadIDs) {
			t.Fatalf("accepted workloads %q", got.WorkloadIDs)
		}
		for i, id := range got.WorkloadIDs {
			if (i > 0 && id == got.WorkloadIDs[i-1]) ||
				(id != WorkloadCPUHashChain && id != WorkloadGPUCUDAMix && id != WorkloadRAMMemoryScan) {
				t.Fatalf("accepted workloads %q", got.WorkloadIDs)
			}
		}
		if _, derived, err := DeriveFederationToken(master, encoded, now); err != nil || !reflect.DeepEqual(derived, got) {
			t.Fatalf("decoded context does not derive a token: %v", err)
		}
	})
}

func FuzzDecodeMotherContext(f *testing.F) {
	issued := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	valid := MotherContext{Version: MotherContextVersion, MotherID: "mother-0123456789abcdef01234567",
		MotherName: "Home Hive (main)", IssuedAt: issued}
	encoded, _, err := EncodeMotherContext(valid)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	canonical, _ := json.Marshal(valid)
	raw := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	f.Add(raw(strings.Replace(string(canonical), "mother-0123", "MOTHER-0123", 1)))
	f.Add(raw(strings.Replace(string(canonical), `"Home Hive (main)"`, `" Home Hive (main)"`, 1)))
	f.Add(raw(strings.Replace(string(canonical), issued, "2026-08-01T14:00:00+02:00", 1)))
	f.Add(raw(strings.Replace(string(canonical), issued, "2999-01-01T00:00:00Z", 1)))
	f.Add(raw(string(canonical) + "\n"))
	f.Add(raw(`{"version":1,"mother_id":"federation-0123456789abcdef01234567","mother_name":"x","issued_at":"2026-08-01T12:00:00Z"}`))
	f.Add("")

	master := bytes.Repeat([]byte{0x62}, 32)
	f.Fuzz(func(t *testing.T, encoded string) {
		got, err := DecodeMotherContext(encoded)
		if err != nil {
			return
		}
		again, normalized, err := EncodeMotherContext(got)
		if err != nil || again != encoded || !reflect.DeepEqual(normalized, got) {
			t.Fatalf("accepted %q but it re-encodes as %q (%v)", encoded, again, err)
		}
		const hexDigits = "0123456789abcdef"
		id := strings.TrimPrefix(got.MotherID, "mother-")
		if got.Version != MotherContextVersion || len(got.MotherID) != len("mother-")+24 || id == got.MotherID ||
			strings.Trim(id, hexDigits) != "" {
			t.Fatalf("accepted version or Mother Hive id: %+v", got)
		}
		if !fuzzIDChars(got.MotherName, 1, 64, " ._()-") {
			t.Fatalf("accepted Mother Hive name %q", got.MotherName)
		}
		at, err := time.Parse(time.RFC3339, got.IssuedAt)
		if err != nil || got.IssuedAt != at.UTC().Format(time.RFC3339) || at.After(time.Now().Add(5*time.Minute)) {
			t.Fatalf("accepted issue time %q", got.IssuedAt)
		}
		if _, derived, err := DeriveMotherToken(master, encoded); err != nil || !reflect.DeepEqual(derived, got) {
			t.Fatalf("decoded context does not derive a token: %v", err)
		}
	})
}

func FuzzVerifyRequestSignature(f *testing.F) {
	body := []byte(`{"hello":"world"}`)
	signature := RequestSignature(testToken(), http.MethodPost, "/v1/register", "1785585600", "nonce-1234567890", "worker-a", body)
	f.Add(testToken(), signature, http.MethodPost, "/v1/register", "1785585600", "nonce-1234567890", "worker-a", body)
	f.Add(testToken(), strings.ToUpper(signature), "post", "/v1/register", "1785585600", "nonce-1234567890", "worker-a", body)
	f.Add(testToken(), signature, http.MethodPost, "/v1/register", "1785585600", "nonce-1234567890", "worker-b", body)
	f.Add(testMotherToken(), signature[:63], http.MethodGet, "/v1/status", "0", "n", "w", []byte(nil))
	f.Add([]byte(nil), "", "", "", "", "", "", []byte(nil))

	f.Fuzz(func(t *testing.T, token []byte, signature, method, path, timestamp, nonce, workerID string, body []byte) {
		got := VerifyRequestSignature(token, signature, method, path, timestamp, nonce, workerID, body)
		expected := RequestSignature(token, method, path, timestamp, nonce, workerID, body)
		// Exactly the HMAC of this request verifies (hex is case-insensitive).
		if got != strings.EqualFold(signature, expected) {
			t.Fatalf("verify(%q) = %v, expected HMAC %s", signature, got, expected)
		}
		if got && VerifyRequestSignature(token, signature, method, path, timestamp, nonce, workerID, append(bytes.Clone(body), 0)) {
			t.Fatal("a request signature verified for a different body")
		}
	})
}

// FuzzVerifyJob decodes a job the way an Agent receives it from its Relay and
// checks both the HMAC and the job limits it then relies on.
func FuzzVerifyJob(f *testing.F) {
	jobs := []Job{
		{Version: ProtocolVersion, ID: "job-cpu-1", WorkerID: "worker-a", Resource: ResourceCPU, Algorithm: AlgorithmCPU,
			Seed: strings.Repeat("ab", 32), Units: MaxCPUUnits, IssuedAt: "2026-08-01T12:00:00Z", ExpiresAt: "2026-08-01T12:05:00Z"},
		{Version: ProtocolVersion, ID: "job-gpu-1", MotherID: "mother-0123456789abcdef01234567", WorkerID: "worker-b",
			Resource: ResourceGPU, Algorithm: AlgorithmGPU, Seed: strings.Repeat("0f", 32), Units: 1},
		{Version: ProtocolVersion, ID: "job-ram-1", WorkerID: "w", Resource: ResourceRAM, Algorithm: AlgorithmRAM,
			Seed: strings.Repeat("11", 32), MemoryMiB: 64, Units: 64 * 1024 * 1024},
	}
	for _, job := range jobs {
		signature, err := SignJob(testToken(), job)
		if err != nil {
			f.Fatal(err)
		}
		job.Signature = signature
		if !VerifyJob(testToken(), job) || ValidateJob(job) != nil {
			f.Fatalf("genuine seed rejected: %+v", job)
		}
		raw, _ := json.Marshal(job)
		f.Add(testToken(), raw)
		f.Add(testMotherToken(), raw)
	}
	f.Add(testToken(), []byte(`{"version":"qsdm-edge-pool/v1","resource":"ram","algorithm":"ram-splitmix64-v1","memory_mib":1025}`))
	f.Add(testToken(), []byte(`{"signature":"zz"}`))

	f.Fuzz(func(t *testing.T, token, raw []byte) {
		var job Job
		if json.Unmarshal(raw, &job) != nil {
			return
		}
		expected, err := SignJob(token, job)
		if err != nil {
			t.Fatalf("SignJob: %v", err)
		}
		if got := VerifyJob(token, job); got != strings.EqualFold(job.Signature, expected) {
			t.Fatalf("VerifyJob = %v for signature %q, expected HMAC %s", got, job.Signature, expected)
		}
		unsigned := job
		unsigned.Signature = ""
		if again, _ := SignJob(token, unsigned); again != expected {
			t.Fatal("the job HMAC covers its own signature field")
		}
		if ValidateJob(job) != nil {
			return
		}
		seed, err := hex.DecodeString(job.Seed)
		if job.Version != ProtocolVersion || job.ID == "" || ValidateWorkerID(job.WorkerID) != nil ||
			err != nil || len(seed) != 32 || job.Units == 0 {
			t.Fatalf("accepted job %+v", job)
		}
		switch job.Resource {
		case ResourceCPU:
			if job.Algorithm != AlgorithmCPU || job.Units > MaxCPUUnits {
				t.Fatalf("accepted CPU job %+v", job)
			}
		case ResourceGPU:
			if job.Algorithm != AlgorithmGPU || job.Units > MaxGPUUnits {
				t.Fatalf("accepted GPU job %+v", job)
			}
		case ResourceRAM:
			if job.Algorithm != AlgorithmRAM || job.MemoryMiB == 0 || job.MemoryMiB > MaxRAMMiB ||
				job.Units != job.MemoryMiB<<20 {
				t.Fatalf("accepted RAM job %+v", job)
			}
		default:
			t.Fatalf("accepted resource %q", job.Resource)
		}
	})
}

func FuzzValidateIDs(f *testing.F) {
	for _, s := range []string{
		"worker-a", "w", "W.1_2-3", strings.Repeat("a", 64), strings.Repeat("a", 65), "-worker", ".x", "",
		"request-12345678", "abcdefgh", "abcdefg", strings.Repeat("R", 128), strings.Repeat("R", 129),
		"worker a", "worker\n", "wörker", "worker:1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, id string) {
		if want := fuzzIDChars(id, 1, 64, "._-"); (ValidateWorkerID(id) == nil) != want {
			t.Fatalf("ValidateWorkerID(%q) accepted=%v, rule says %v", id, !want, want)
		}
		if want := fuzzIDChars(id, 8, 128, "._-"); (ValidateComputeRequestID(id) == nil) != want {
			t.Fatalf("ValidateComputeRequestID(%q) accepted=%v, rule says %v", id, !want, want)
		}
	})
}
