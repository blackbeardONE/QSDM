package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// captureServer returns an httptest server that records the
// last request seen, for verifying the CLI sends the right
// shape. The server returns 202 Accepted with a static body
// — the CLI just pretty-prints whatever the server returns,
// so behaviour is independent of body content.
type captureServer struct {
	t      *testing.T
	server *httptest.Server

	method      string
	path        string // url.Path — host-decoded, useful for routing assertions
	rawPath     string // url.EscapedPath() — preserves percent-encoding
	rawQuery    string // r.URL.RawQuery — preserves query-string ordering
	contentType string
	body        []byte
	status      int

	// hits counts every request the server has answered.
	// Used by --all walk tests to assert the CLI paged the
	// expected number of times.
	hits int

	// responses, if non-nil, supplies a different body per
	// request (responses[hits-1]). Tests for --all walks
	// install successive page bodies so the CLI sees a
	// realistic multi-page sequence.
	responses []string
}

func newCaptureServer(t *testing.T, status int, response string) *captureServer {
	t.Helper()
	cs := &captureServer{t: t, status: status}
	cs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.method = r.Method
		cs.path = r.URL.Path
		cs.rawPath = r.URL.EscapedPath()
		cs.rawQuery = r.URL.RawQuery
		cs.contentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			r.Body.Read(buf)
		}
		cs.body = buf
		w.WriteHeader(status)
		// Supply per-request bodies if configured; otherwise
		// fall back to the static response from construction.
		// Tests that drive --all walks rely on this so each
		// hit returns the next page.
		idx := cs.hits
		cs.hits++
		body := response
		if idx < len(cs.responses) {
			body = cs.responses[idx]
		}
		_, _ = w.Write([]byte(body))
	}))
	return cs
}

func (cs *captureServer) close() { cs.server.Close() }

func (cs *captureServer) cli() *CLI {
	return &CLI{baseURL: cs.server.URL + "/api/v1", client: http.DefaultClient}
}

func TestMiningEnroll_BuildsCanonicalEnvelope(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{"tx_id":"abc","status":"accepted"}`)
	defer cs.close()

	hmacHex := strings.Repeat("ab", 32)
	args := []string{
		"--sender", "alice",
		"--node-id", "rig-77",
		"--gpu-uuid", "GPU-12345678-1234-1234-1234-123456789abc",
		"--hmac-key", hmacHex,
		"--nonce", "5",
		"--fee", "0.001",
		"--id", "tx-enroll-deterministic",
		"--memo", "smoke",
	}
	if err := cs.cli().miningEnroll(args); err != nil {
		t.Fatalf("miningEnroll: %v", err)
	}
	if cs.method != http.MethodPost {
		t.Errorf("method: got %s, want POST", cs.method)
	}
	if cs.path != "/api/v1/mining/enroll" {
		t.Errorf("path: got %s, want /api/v1/mining/enroll", cs.path)
	}
	if !strings.HasPrefix(cs.contentType, "application/json") {
		t.Errorf("content-type: got %q, want application/json", cs.contentType)
	}

	var env map[string]any
	if err := json.Unmarshal(cs.body, &env); err != nil {
		t.Fatalf("body not JSON: %v body=%s", err, cs.body)
	}
	if env["sender"] != "alice" {
		t.Errorf("sender: got %v, want alice", env["sender"])
	}
	if env["id"] != "tx-enroll-deterministic" {
		t.Errorf("id not preserved: %v", env["id"])
	}
	if env["contract_id"] != enrollment.ContractID {
		t.Errorf("contract_id: got %v, want %s", env["contract_id"], enrollment.ContractID)
	}

	rawB64, _ := env["payload_b64"].(string)
	raw, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		t.Fatalf("payload_b64 not base64: %v", err)
	}
	got, err := enrollment.DecodeEnrollPayload(raw)
	if err != nil {
		t.Fatalf("payload not valid EnrollPayload: %v", err)
	}
	wantHMAC, _ := hex.DecodeString(hmacHex)
	if got.NodeID != "rig-77" ||
		got.GPUUUID != "GPU-12345678-1234-1234-1234-123456789abc" ||
		string(got.HMACKey) != string(wantHMAC) ||
		got.StakeDust != mining.MinEnrollStakeDust ||
		got.Memo != "smoke" {
		t.Errorf("payload fields: %+v", got)
	}
}

func TestMiningEnroll_RandomIDWhenNoneProvided(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{"tx_id":"x"}`)
	defer cs.close()

	hmacHex := strings.Repeat("cd", 32)
	args := []string{
		"--sender", "alice", "--node-id", "rig-77",
		"--gpu-uuid", "GPU-12345678-1234-1234-1234-123456789abc",
		"--hmac-key", hmacHex,
	}
	if err := cs.cli().miningEnroll(args); err != nil {
		t.Fatalf("miningEnroll: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(cs.body, &env); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	id, _ := env["id"].(string)
	if len(id) != 32 {
		t.Errorf("auto-id should be 32 hex chars (16 bytes); got %q (len %d)", id, len(id))
	}
}

func TestMiningEnroll_RejectsMissingFlags(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{}`)
	defer cs.close()
	cli := cs.cli()

	tests := [][]string{
		{}, // all missing
		{"--sender", "x"},
		{"--sender", "x", "--node-id", "y"},
		{"--sender", "x", "--node-id", "y", "--gpu-uuid", "z"},
	}
	for _, args := range tests {
		if err := cli.miningEnroll(args); err == nil {
			t.Errorf("missing required flags accepted: %v", args)
		}
	}
}

func TestMiningEnroll_RejectsBadHMACHex(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{}`)
	defer cs.close()
	args := []string{
		"--sender", "alice", "--node-id", "rig-77",
		"--gpu-uuid", "GPU-12345678-1234-1234-1234-123456789abc",
		"--hmac-key", "not-hex-zzz",
	}
	err := cs.cli().miningEnroll(args)
	if err == nil || !strings.Contains(err.Error(), "hex") {
		t.Errorf("bad hmac-key hex not surfaced: %v", err)
	}
}

func TestMiningUnenroll_BuildsCanonicalEnvelope(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{"tx_id":"u1","status":"accepted"}`)
	defer cs.close()

	args := []string{
		"--sender", "alice", "--node-id", "rig-77",
		"--reason", "decommissioning", "--nonce", "1", "--fee", "0.002",
		"--id", "tx-unenroll-1",
	}
	if err := cs.cli().miningUnenroll(args); err != nil {
		t.Fatalf("miningUnenroll: %v", err)
	}
	if cs.path != "/api/v1/mining/unenroll" {
		t.Errorf("path: got %s, want /api/v1/mining/unenroll", cs.path)
	}

	var env map[string]any
	json.Unmarshal(cs.body, &env)
	if env["contract_id"] != enrollment.ContractID {
		t.Errorf("contract_id: %v", env["contract_id"])
	}
	rawB64, _ := env["payload_b64"].(string)
	raw, _ := base64.StdEncoding.DecodeString(rawB64)
	got, err := enrollment.DecodeUnenrollPayload(raw)
	if err != nil {
		t.Fatalf("payload not valid UnenrollPayload: %v", err)
	}
	if got.NodeID != "rig-77" || got.Reason != "decommissioning" {
		t.Errorf("payload fields: %+v", got)
	}
}

func TestMiningUnenroll_RejectsMissingFlags(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{}`)
	defer cs.close()
	if err := cs.cli().miningUnenroll(nil); err == nil {
		t.Error("missing required flags accepted")
	}
}

func TestMiningSlash_BuildsCanonicalEnvelope(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{"tx_id":"s1","status":"accepted"}`)
	defer cs.close()

	evidenceHex := hex.EncodeToString([]byte("opaque-evidence"))
	args := []string{
		"--sender", "watcher", "--node-id", "rig-77",
		"--evidence-kind", "forged-attestation",
		"--evidence-hex", evidenceHex,
		"--amount", "500000000", // 5 CELL
		"--memo", "caught red-handed",
		"--nonce", "0", "--fee", "0.001",
		"--id", "tx-slash-1",
	}
	if err := cs.cli().miningSlash(args); err != nil {
		t.Fatalf("miningSlash: %v", err)
	}
	if cs.path != "/api/v1/mining/slash" {
		t.Errorf("path: got %s, want /api/v1/mining/slash", cs.path)
	}

	var env map[string]any
	json.Unmarshal(cs.body, &env)
	if env["contract_id"] != slashing.ContractID {
		t.Errorf("contract_id: %v", env["contract_id"])
	}
	rawB64, _ := env["payload_b64"].(string)
	raw, _ := base64.StdEncoding.DecodeString(rawB64)
	got, err := slashing.DecodeSlashPayload(raw)
	if err != nil {
		t.Fatalf("payload not valid SlashPayload: %v", err)
	}
	if got.NodeID != "rig-77" ||
		got.EvidenceKind != slashing.EvidenceKindForgedAttestation ||
		got.SlashAmountDust != 500_000_000 ||
		string(got.EvidenceBlob) != "opaque-evidence" {
		t.Errorf("payload fields: %+v", got)
	}
}

func TestMiningSlash_RejectsMissingEvidence(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{}`)
	defer cs.close()
	args := []string{
		"--sender", "watcher", "--node-id", "rig-77",
		"--evidence-kind", "forged-attestation",
		"--amount", "1",
	}
	err := cs.cli().miningSlash(args)
	if err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Errorf("missing evidence not surfaced: %v", err)
	}
}

func TestMiningSlash_RejectsMissingFlags(t *testing.T) {
	cs := newCaptureServer(t, http.StatusAccepted, `{}`)
	defer cs.close()
	cli := cs.cli()

	tests := [][]string{
		{},
		{"--sender", "w"},
		{"--sender", "w", "--node-id", "n"},
		{"--sender", "w", "--node-id", "n", "--evidence-kind", "forged-attestation"},
		{"--sender", "w", "--node-id", "n", "--evidence-kind", "forged-attestation", "--amount", "0"},
	}
	for _, args := range tests {
		if err := cli.miningSlash(args); err == nil {
			t.Errorf("missing required flags accepted: %v", args)
		}
	}
}

func TestMiningEnrollmentStatus_HitsGetEndpoint(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, `{"node_id":"rig-77","phase":"active","slashable":true}`)
	defer cs.close()

	if err := cs.cli().miningEnrollmentStatus([]string{"rig-77"}); err != nil {
		t.Fatalf("miningEnrollmentStatus: %v", err)
	}
	if cs.method != http.MethodGet {
		t.Errorf("method: got %s, want GET", cs.method)
	}
	if cs.path != "/api/v1/mining/enrollment/rig-77" {
		t.Errorf("path: got %s, want /api/v1/mining/enrollment/rig-77", cs.path)
	}
}

func TestMiningEnrollmentStatus_NodeIDIsPathEscaped(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, `{}`)
	defer cs.close()

	// NodeIDs validate as URL-safe at the chain layer, but
	// the CLI must still escape correctly so a hypothetical
	// '%' or space doesn't break the request.
	if err := cs.cli().miningEnrollmentStatus([]string{"rig-with-space test"}); err != nil {
		t.Fatalf("miningEnrollmentStatus: %v", err)
	}
	if !strings.Contains(cs.rawPath, "rig-with-space%20test") {
		t.Errorf("node-id not URL-escaped in path: rawPath=%q path=%q", cs.rawPath, cs.path)
	}
}

func TestMiningEnrollmentStatus_RejectsEmptyNodeID(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, `{}`)
	defer cs.close()
	if err := cs.cli().miningEnrollmentStatus(nil); err == nil {
		t.Error("missing positional accepted")
	}
	if err := cs.cli().miningEnrollmentStatus([]string{""}); err == nil {
		t.Error("empty positional accepted")
	}
}

func TestMiningEnrollmentStatus_RejectsSlashInNodeID(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, `{}`)
	defer cs.close()
	if err := cs.cli().miningEnrollmentStatus([]string{"foo/bar"}); err == nil {
		t.Error("nested node-id accepted; should reject early")
	}
}

func TestMiningSlashReceipt_HitsGetEndpoint(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK,
		`{"tx_id":"tx-abc","outcome":"applied","height":42}`)
	defer cs.close()

	if err := cs.cli().miningSlashReceipt([]string{"tx-abc"}); err != nil {
		t.Fatalf("miningSlashReceipt: %v", err)
	}
	if cs.method != http.MethodGet {
		t.Errorf("method: got %s, want GET", cs.method)
	}
	if cs.path != "/api/v1/mining/slash/tx-abc" {
		t.Errorf("path: got %s, want /api/v1/mining/slash/tx-abc", cs.path)
	}
}

func TestMiningSlashReceipt_TxIDIsPathEscaped(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, `{}`)
	defer cs.close()

	// Tx ids in the wild are usually hex, but the CLI must
	// still escape correctly so a hypothetical '%' or space
	// doesn't break the request.
	if err := cs.cli().miningSlashReceipt([]string{"tx with space"}); err != nil {
		t.Fatalf("miningSlashReceipt: %v", err)
	}
	if !strings.Contains(cs.rawPath, "tx%20with%20space") {
		t.Errorf("tx-id not URL-escaped in path: rawPath=%q path=%q", cs.rawPath, cs.path)
	}
}

func TestMiningSlashReceipt_RejectsEmptyTxID(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, `{}`)
	defer cs.close()
	if err := cs.cli().miningSlashReceipt(nil); err == nil {
		t.Error("missing positional accepted")
	}
	if err := cs.cli().miningSlashReceipt([]string{""}); err == nil {
		t.Error("empty positional accepted")
	}
}

func TestMiningSlashReceipt_RejectsSlashInTxID(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, `{}`)
	defer cs.close()
	if err := cs.cli().miningSlashReceipt([]string{"foo/bar"}); err == nil {
		t.Error("nested tx-id accepted; should reject early")
	}
}

func TestMiningSlashReceipt_PropagatesHTTPError(t *testing.T) {
	cs := newCaptureServer(t, http.StatusNotFound, "no slash receipt for tx_id (unknown or evicted)")
	defer cs.close()

	err := cs.cli().miningSlashReceipt([]string{"tx-missing"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404 not propagated: %v", err)
	}
}

func TestMiningEnroll_PropagatesHTTPError(t *testing.T) {
	cs := newCaptureServer(t, http.StatusBadRequest, "bad payload")
	defer cs.close()

	err := cs.cli().miningEnroll([]string{
		"--sender", "alice", "--node-id", "rig-77",
		"--gpu-uuid", "GPU-12345678-1234-1234-1234-123456789abc",
		"--hmac-key", strings.Repeat("ab", 32),
	})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("400 not propagated: %v", err)
	}
}

func TestGenerateTxID_LengthAndUniqueness(t *testing.T) {
	a := generateTxID()
	b := generateTxID()
	if len(a) != 32 || len(b) != 32 {
		t.Errorf("expected 32-hex IDs, got %q %q", a, b)
	}
	if a == b {
		t.Error("generateTxID returned same id twice (extremely unlikely)")
	}
}

func TestReadEvidenceBytes_HexPath(t *testing.T) {
	got, err := readEvidenceBytes("", hex.EncodeToString([]byte("hello")))
	if err != nil {
		t.Fatalf("hex path: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("decoded: got %q, want hello", got)
	}
}

func TestReadEvidenceBytes_RejectsMissing(t *testing.T) {
	_, err := readEvidenceBytes("", "")
	if err == nil {
		t.Error("missing evidence flags accepted")
	}
}

// -----------------------------------------------------------------------------
// enrollments (paginated list)
// -----------------------------------------------------------------------------

func TestMiningEnrollmentsList_HitsGetEndpoint(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK,
		`{"records":[],"has_more":false,"total_matches":0}`)
	defer cs.close()

	if err := cs.cli().miningEnrollmentsList(nil); err != nil {
		t.Fatalf("miningEnrollmentsList: %v", err)
	}
	if cs.method != http.MethodGet {
		t.Errorf("method: got %s, want GET", cs.method)
	}
	if cs.path != "/api/v1/mining/enrollments" {
		t.Errorf("path: got %s, want /api/v1/mining/enrollments", cs.path)
	}
	if cs.rawQuery != "" {
		t.Errorf("query: got %q, want empty for default invocation", cs.rawQuery)
	}
}

func TestMiningEnrollmentsList_PassesAllFlagsAsQueryParams(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK,
		`{"records":[],"has_more":false,"total_matches":0}`)
	defer cs.close()

	args := []string{
		"--phase", "active",
		"--limit", "25",
		"--cursor", "rig-077",
	}
	if err := cs.cli().miningEnrollmentsList(args); err != nil {
		t.Fatalf("miningEnrollmentsList: %v", err)
	}
	q := cs.rawQuery
	for _, want := range []string{"phase=active", "limit=25", "cursor=rig-077"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: got %q", want, q)
		}
	}
}

func TestMiningEnrollmentsList_AllWalksUntilHasMoreFalse(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, "")
	defer cs.close()
	cs.responses = []string{
		`{"records":[{"node_id":"rig-1"}],"next_cursor":"rig-1","has_more":true,"total_matches":3}`,
		`{"records":[{"node_id":"rig-2"}],"next_cursor":"rig-2","has_more":true,"total_matches":3}`,
		`{"records":[{"node_id":"rig-3"}],"next_cursor":"","has_more":false,"total_matches":3}`,
	}

	if err := cs.cli().miningEnrollmentsList([]string{"--all"}); err != nil {
		t.Fatalf("miningEnrollmentsList: %v", err)
	}
	if cs.hits != 3 {
		t.Errorf("expected 3 server hits, got %d", cs.hits)
	}
}

func TestMiningEnrollmentsList_AllForwardsNextCursor(t *testing.T) {
	cs := newCaptureServer(t, http.StatusOK, "")
	defer cs.close()
	type req struct{ rawQuery string }
	requests := make([]req, 0, 3)
	cs.server.Close() // rebuild the server so we can record per-request queries
	cs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, req{rawQuery: r.URL.RawQuery})
		idx := len(requests) - 1
		bodies := []string{
			`{"records":[{"node_id":"rig-1"}],"next_cursor":"rig-1","has_more":true,"total_matches":2}`,
			`{"records":[{"node_id":"rig-2"}],"next_cursor":"","has_more":false,"total_matches":2}`,
		}
		body := `{"records":[],"has_more":false,"total_matches":0}`
		if idx < len(bodies) {
			body = bodies[idx]
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))

	if err := cs.cli().miningEnrollmentsList([]string{"--all"}); err != nil {
		t.Fatalf("miningEnrollmentsList: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}
	if requests[0].rawQuery != "" {
		t.Errorf("first request must omit cursor; got %q", requests[0].rawQuery)
	}
	if !strings.Contains(requests[1].rawQuery, "cursor=rig-1") {
		t.Errorf("second request must carry cursor=rig-1; got %q", requests[1].rawQuery)
	}
}

func TestMiningEnrollmentsList_AllRejectsBuggyServer(t *testing.T) {
	// Server returns has_more=true with empty next_cursor —
	// the CLI must surface this as an error rather than spin.
	cs := newCaptureServer(t, http.StatusOK,
		`{"records":[{"node_id":"rig-1"}],"next_cursor":"","has_more":true,"total_matches":99}`)
	defer cs.close()
	err := cs.cli().miningEnrollmentsList([]string{"--all"})
	if err == nil {
		t.Fatal("CLI accepted has_more=true with empty next_cursor; would spin")
	}
	if !strings.Contains(err.Error(), "next_cursor") {
		t.Errorf("error should mention next_cursor: %v", err)
	}
}

func TestMiningEnrollmentsList_PropagatesHTTPError(t *testing.T) {
	cs := newCaptureServer(t, http.StatusServiceUnavailable,
		"v2 enrollment lister not configured on this node")
	defer cs.close()
	err := cs.cli().miningEnrollmentsList(nil)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("503 not propagated: %v", err)
	}
}
