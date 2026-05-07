package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

// buildTelemetryServer is a tiny harness that gives a Server
// pre-wired with a FixedCollector + in-memory registry +
// the same key as the challenge signer. Returns the live
// httptest server URL + the underlying provider for
// assertions.
func buildTelemetryServer(t *testing.T, observations []telemetry.GPUObservation) (string, *TelemetryProvider, func()) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	signer, err := challenge.NewHMACSigner("attester-test-deadbeef0001", key)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	issuer, err := challenge.NewIssuer(signer)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	cfg := &Config{ListenAddr: "127.0.0.1:0", Note: "test-host"}
	srv, err := NewServer(cfg, issuer, signer, "fingerprintXYZ")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	reg, err := telemetry.NewRegistry(signer.SignerID(), "test-host", "fixed")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, err := reg.ApplyAll(observations); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	provider := &TelemetryProvider{
		Registry:    reg,
		Key:         key,
		PersistPath: "",
	}
	srv.SetTelemetry(provider)

	ts := httptest.NewServer(srv.Routes())
	return ts.URL, provider, ts.Close
}

func TestTelemetryReference_Disabled404(t *testing.T) {
	key := make([]byte, 32)
	signer, _ := challenge.NewHMACSigner("attester-x", key)
	issuer, _ := challenge.NewIssuer(signer)
	srv, _ := NewServer(&Config{ListenAddr: "127.0.0.1:0", Note: "n"}, issuer, signer, "fp")
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/v1/telemetry/reference")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (telemetry_disabled)", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != "telemetry_disabled" {
		t.Fatalf("body = %v", body)
	}
}

func TestTelemetryReference_HappyPath(t *testing.T) {
	url, provider, cleanup := buildTelemetryServer(t, []telemetry.GPUObservation{
		{
			UUID:               "GPU-39925fa6-82f0-0e13-dd28-aa4be2048287",
			Name:               "NVIDIA GeForce RTX 3050",
			Vendor:             "NVIDIA",
			Architecture:       "ampere",
			ComputeCapability:  "8.6",
			MemoryTotalMB:      8188,
			DriverVersionsSeen: []string{"576.28"},
		},
	})
	t.Cleanup(cleanup)

	resp, err := http.Get(url + "/api/v1/telemetry/reference")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var p telemetry.ReferenceProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.SignerID != "attester-test-deadbeef0001" {
		t.Fatalf("signer = %q", p.SignerID)
	}
	if !p.Verify(provider.Key) {
		t.Fatalf("Verify rejected the signed profile")
	}
	if len(p.GPUs) != 1 || p.GPUs[0].Name != "NVIDIA GeForce RTX 3050" {
		t.Fatalf("gpus = %+v", p.GPUs)
	}
	if p.IssuedAt < time.Now().Unix()-5 {
		t.Fatalf("IssuedAt suspicious: %d (expected near %d)", p.IssuedAt, time.Now().Unix())
	}
	if provider.requests.Load() != 1 {
		t.Fatalf("provider.requests = %d", provider.requests.Load())
	}
}

func TestTelemetryReference_GPUFilter(t *testing.T) {
	url, provider, cleanup := buildTelemetryServer(t, []telemetry.GPUObservation{
		{UUID: "GPU-a", Name: "RTX 3050", MemoryTotalMB: 8188},
		{UUID: "GPU-b", Name: "RTX 4090", MemoryTotalMB: 24564},
	})
	t.Cleanup(cleanup)

	resp, err := http.Get(url + "/api/v1/telemetry/reference?gpu=GPU-a")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var p telemetry.ReferenceProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.GPUs) != 1 || p.GPUs[0].UUID != "GPU-a" {
		t.Fatalf("filter returned %+v", p.GPUs)
	}
	// Critical: the signature must verify after filtering
	// (handler re-signs after the filter pass).
	if !p.Verify(provider.Key) {
		t.Fatalf("filtered profile failed verification")
	}
}

func TestTelemetryReference_ObservationCap(t *testing.T) {
	url, provider, cleanup := buildTelemetryServer(t, []telemetry.GPUObservation{
		{
			UUID:               "GPU-a",
			Name:               "RTX 3050",
			DriverVersionsSeen: []string{"v1", "v2", "v3", "v4", "v5"},
		},
	})
	t.Cleanup(cleanup)

	resp, err := http.Get(url + "/api/v1/telemetry/reference?include_observations=2")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var p telemetry.ReferenceProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.GPUs[0].DriverVersionsSeen) != 2 {
		t.Fatalf("cap not applied: %v", p.GPUs[0].DriverVersionsSeen)
	}
	if !p.Verify(provider.Key) {
		t.Fatalf("capped profile failed verification")
	}
}

func TestTelemetryReference_BadObservationCap(t *testing.T) {
	url, _, cleanup := buildTelemetryServer(t, []telemetry.GPUObservation{
		{UUID: "GPU-a", Name: "RTX 3050"},
	})
	t.Cleanup(cleanup)

	resp, err := http.Get(url + "/api/v1/telemetry/reference?include_observations=not-a-number")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestTelemetryReference_RejectsNonGET(t *testing.T) {
	url, _, cleanup := buildTelemetryServer(t, nil)
	t.Cleanup(cleanup)

	req, err := http.NewRequest(http.MethodPost, url+"/api/v1/telemetry/reference", strings.NewReader(""))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", resp.StatusCode)
	}
}

func TestInfo_TelemetryFieldsPopulated(t *testing.T) {
	url, _, cleanup := buildTelemetryServer(t, []telemetry.GPUObservation{
		{UUID: "GPU-a", Name: "RTX 3050"},
		{UUID: "GPU-b", Name: "RTX 4090"},
	})
	t.Cleanup(cleanup)

	resp, err := http.Get(url + "/info")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var info InfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !info.TelemetryEnabled {
		t.Fatalf("TelemetryEnabled false")
	}
	if info.TelemetryGPUs != 2 {
		t.Fatalf("TelemetryGPUs = %d", info.TelemetryGPUs)
	}
}

func TestMetrics_TelemetryCounters(t *testing.T) {
	url, _, cleanup := buildTelemetryServer(t, []telemetry.GPUObservation{
		{UUID: "GPU-a", Name: "RTX 3050"},
	})
	t.Cleanup(cleanup)

	// Hit the telemetry endpoint twice so the counter is non-zero.
	for i := 0; i < 2; i++ {
		resp, err := http.Get(url + "/api/v1/telemetry/reference")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		resp.Body.Close()
	}

	resp, err := http.Get(url + "/metrics")
	if err != nil {
		t.Fatalf("GET metrics: %v", err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	for _, want := range []string{
		"qsdm_attester_telemetry_gpus",
		"qsdm_attester_telemetry_collection_ticks_total",
		"qsdm_attester_telemetry_apply_calls_total",
		"qsdm_attester_telemetry_requests_total",
		"qsdm_attester_telemetry_sign_failures_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metric %q missing from body", want)
		}
	}
	// Spot-check the request counter shows >=2.
	if !strings.Contains(body, `qsdm_attester_telemetry_requests_total{signer_id="attester-test-deadbeef0001"} 2`) {
		t.Errorf("requests counter missing or unexpected:\n%s", body)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			break
		}
	}
	return string(b)
}

// runTelemetryCollector + tickOnce: drive the goroutine
// briefly via a FixedCollector to prove the wiring + the
// persistence handoff.

func TestRunTelemetryCollector_PersistsAndCounts(t *testing.T) {
	dir := t.TempDir()
	persistPath := filepath.Join(dir, "telemetry.json")

	reg, _ := telemetry.NewRegistry("attester-x", "host-x", "fixed")
	provider := &TelemetryProvider{
		Registry:    reg,
		Key:         make([]byte, 32),
		PersistPath: persistPath,
	}
	for i := range provider.Key {
		provider.Key[i] = 0x01
	}

	collector := &telemetry.FixedCollector{
		KindStr: "fixed",
		Observations: []telemetry.GPUObservation{
			{UUID: "GPU-a", Name: "RTX 3050", MemoryTotalMB: 8188},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	runTelemetryCollector(ctx, reg, collector, persistPath, 100*time.Millisecond, provider, nil)

	if provider.collectionTicks.Load() < 1 {
		t.Fatalf("ticks = %d", provider.collectionTicks.Load())
	}
	if _, err := readFileSize(persistPath); err != nil {
		t.Fatalf("persist file: %v", err)
	}
}

func TestTickOnce_PropagatesCollectError(t *testing.T) {
	reg, _ := telemetry.NewRegistry("attester-x", "host", "fixed")
	provider := &TelemetryProvider{Registry: reg, Key: make([]byte, 32)}
	want := errors.New("boom")
	collector := &telemetry.FixedCollector{Err: want}
	if err := tickOnce(context.Background(), reg, collector, "", provider, nil); !errors.Is(err, want) {
		t.Fatalf("expected error propagation, got %v", err)
	}
	if provider.collectionErrs.Load() == 0 {
		t.Fatalf("error counter did not increment")
	}
}

func TestParseUintQuery(t *testing.T) {
	cases := map[string]uint64{
		"0":   0,
		"1":   1,
		"42":  42,
		"100": 100,
	}
	for in, want := range cases {
		got, err := parseUintQuery(in)
		if err != nil {
			t.Errorf("parseUintQuery(%q) err: %v", in, err)
		}
		if got != want {
			t.Errorf("parseUintQuery(%q) = %d want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "-1", "12 ", " 12"} {
		if _, err := parseUintQuery(bad); err == nil {
			t.Errorf("parseUintQuery(%q) accepted bad input", bad)
		}
	}
}

func TestResolveTelemetryPath(t *testing.T) {
	if p, err := resolveTelemetryPath("-"); err != nil || p != "" {
		t.Errorf(`"-" → (%q, %v) want ("", nil)`, p, err)
	}
	if p, err := resolveTelemetryPath("/abs/path.json"); err != nil || p != "/abs/path.json" {
		t.Errorf("explicit path: %q, %v", p, err)
	}
	if p, err := resolveTelemetryPath(""); err != nil || p == "" {
		t.Errorf("default path failed: %q, %v", p, err)
	}
}

// readFileSize confirms the persisted file actually
// materialised on disk. Errors propagate from os.Stat
// (mostly: not-found).
func readFileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
