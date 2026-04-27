package main

// mining.go — v2 mining-protocol subcommands for qsdmcli.
//
// Surfaces the four HTTP endpoints landed in
// pkg/api/handlers_{enrollment,slashing,enrollment_query}.go
// behind ergonomic CLI verbs:
//
//	qsdmcli enroll              POST /api/v1/mining/enroll
//	qsdmcli unenroll            POST /api/v1/mining/unenroll
//	qsdmcli slash               POST /api/v1/mining/slash
//	qsdmcli enrollment-status   GET  /api/v1/mining/enrollment/{node_id}
//
// Why one CLI file (not four):
//
//   - All four share the same envelope-construction pattern
//     (build canonical payload → base64-encode → wrap in
//     {ID, Sender, Nonce, Fee, ContractID, PayloadB64}).
//     Centralising in one file keeps the wrapper logic
//     visibly identical so a future protocol-version bump
//     touches one place, not four.
//   - The argument-parsing surface is similar enough that
//     readers can compare flag sets at a glance.
//
// Why dedicated commands rather than asking miners to use
// `qsdmcli tx` plus a raw payload:
//
//   - Building a canonical SlashPayload by hand requires
//     getting JSON field order right (canonicaljson contract),
//     base64-encoding correctly, and computing the right
//     contract_id literal. Every step is a footgun for an
//     operator under stress.
//   - The CLI uses pkg/mining/enrollment + pkg/mining/slashing
//     directly, so the canonical-form contract is produced by
//     exactly the same code the mempool admission gate uses
//     to validate it. There's no second path that can drift.
//
// Signing model: this CLI does NOT cryptographically sign the
// envelope. The validator's AccountStore identifies the sender
// by string and debits balance + nonce; replay protection is
// provided by the mempool's tx-id deduplication + the chain's
// nonce ordering. This matches the existing `qsdmcli tx` shape.
// Future work (wallet / envelope-signing roadmap; tracked
// alongside MINING_PROTOCOL_V2.md §13.5) may add Dilithium-signed
// envelopes; when it does, this file gains a single signing call
// inside buildEnvelope().

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// envelope is the wire shape POST'd to all three write
// endpoints. Identical key set across enroll / unenroll /
// slash because the validator handlers consume the same
// EnrollmentSubmitRequest / SlashSubmitRequest struct shape.
//
// Tag-keyed JSON marshalling is provided by the existing
// CLI.post() helper; we don't define explicit struct tags
// here because the CLI's interface{} payload path Marshals
// map keys directly.
type envelope = map[string]interface{}

// generateTxID returns a 16-byte random hex string. Used
// when the operator does not supply --id explicitly. The id
// is the mempool-level deduplication key; collisions just
// mean the second submission gets HTTP 409 Conflict, so the
// random id only needs uniqueness within the in-flight
// window for one operator (16 bytes = 128 bits of entropy
// is overkill, but cheap).
func generateTxID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failures are essentially impossible on
		// real platforms; if we hit one, fall back to a
		// readable tag so the user can see what happened
		// rather than crash with a misleading error later.
		return "qsdmcli-rand-failed"
	}
	return hex.EncodeToString(b[:])
}

// readEvidenceBytes loads evidence-blob bytes from one of
// the three CLI sources we support, in order of precedence:
//
//	--evidence-file=PATH   raw bytes from disk
//	--evidence-hex=HEX     hex-decoded bytes
//	(no flag)              error: evidence is required
//
// "-" as the file path means stdin, mirroring standard Unix
// idiom. Stdin is useful for piping a slasher tool's output
// directly into qsdmcli without a temp file.
func readEvidenceBytes(filePath, hexStr string) ([]byte, error) {
	if filePath != "" {
		if filePath == "-" {
			return io.ReadAll(os.Stdin)
		}
		return os.ReadFile(filePath)
	}
	if hexStr != "" {
		return hex.DecodeString(hexStr)
	}
	return nil, fmt.Errorf("provide one of --evidence-file or --evidence-hex")
}

// -----------------------------------------------------------------------------
// enroll
// -----------------------------------------------------------------------------

// miningEnroll handles `qsdmcli enroll`. Builds a canonical
// EnrollPayload, base64-wraps it in the standard envelope,
// and POSTs to /mining/enroll.
//
// Required flags: --sender, --node-id, --gpu-uuid, --hmac-key.
// hmac-key is HEX-encoded on the wire; the on-chain record
// stores raw bytes.
func (c *CLI) miningEnroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		sender  = fs.String("sender", "", "account address that will own the enrollment (required)")
		nodeID  = fs.String("node-id", "", "operator-chosen NodeID for the rig (required)")
		gpuUUID = fs.String("gpu-uuid", "", "NVIDIA GPU UUID, e.g. GPU-12345678-... (required)")
		hmacHex = fs.String("hmac-key", "", "32-byte HMAC key, hex-encoded (required)")
		stake   = fs.Uint64("stake", mining.MinEnrollStakeDust,
			"bond amount in dust (default = mining.MinEnrollStakeDust = 10 CELL)")
		nonce = fs.Uint64("nonce", 0, "account nonce; must match validator-side AccountStore")
		fee   = fs.Float64("fee", 0.001, "tx fee in CELL")
		memo  = fs.String("memo", "", "optional human-readable memo (≤256 bytes)")
		txID  = fs.String("id", "", "mempool tx id (default = random hex)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sender == "" || *nodeID == "" || *gpuUUID == "" || *hmacHex == "" {
		fs.Usage()
		return fmt.Errorf("--sender, --node-id, --gpu-uuid, --hmac-key are required")
	}

	hmacKey, err := hex.DecodeString(*hmacHex)
	if err != nil {
		return fmt.Errorf("--hmac-key must be valid hex: %w", err)
	}

	payload := enrollment.EnrollPayload{
		Kind:      enrollment.PayloadKindEnroll,
		NodeID:    *nodeID,
		GPUUUID:   *gpuUUID,
		HMACKey:   hmacKey,
		StakeDust: *stake,
		Memo:      *memo,
	}
	raw, err := enrollment.EncodeEnrollPayload(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	id := *txID
	if id == "" {
		id = generateTxID()
	}
	body, err := c.post("/mining/enroll", envelope{
		"id":          id,
		"sender":      *sender,
		"nonce":       *nonce,
		"fee":         *fee,
		"contract_id": enrollment.ContractID,
		"payload_b64": base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

// -----------------------------------------------------------------------------
// unenroll
// -----------------------------------------------------------------------------

// miningUnenroll handles `qsdmcli unenroll`. Mirror of
// miningEnroll for the UnenrollPayload contract. Begins the
// 7-day unbond — bond is NOT released immediately.
func (c *CLI) miningUnenroll(args []string) error {
	fs := flag.NewFlagSet("unenroll", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		sender = fs.String("sender", "", "account address that owns the enrollment (required)")
		nodeID = fs.String("node-id", "", "NodeID to retire (required)")
		reason = fs.String("reason", "", "optional human-readable reason (≤256 bytes)")
		nonce  = fs.Uint64("nonce", 0, "account nonce; must match validator-side AccountStore")
		fee    = fs.Float64("fee", 0.001, "tx fee in CELL")
		txID   = fs.String("id", "", "mempool tx id (default = random hex)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sender == "" || *nodeID == "" {
		fs.Usage()
		return fmt.Errorf("--sender and --node-id are required")
	}

	payload := enrollment.UnenrollPayload{
		Kind:   enrollment.PayloadKindUnenroll,
		NodeID: *nodeID,
		Reason: *reason,
	}
	raw, err := enrollment.EncodeUnenrollPayload(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	id := *txID
	if id == "" {
		id = generateTxID()
	}
	body, err := c.post("/mining/unenroll", envelope{
		"id":          id,
		"sender":      *sender,
		"nonce":       *nonce,
		"fee":         *fee,
		"contract_id": enrollment.ContractID,
		"payload_b64": base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

// -----------------------------------------------------------------------------
// slash
// -----------------------------------------------------------------------------

// miningSlash handles `qsdmcli slash`. Builds a canonical
// SlashPayload from operator-supplied evidence and POSTs to
// /mining/slash. The submitter need not be the offender's
// owner; any peer can submit evidence.
func (c *CLI) miningSlash(args []string) error {
	fs := flag.NewFlagSet("slash", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		sender       = fs.String("sender", "", "account address submitting the evidence (required)")
		nodeID       = fs.String("node-id", "", "offender NodeID (required)")
		kind         = fs.String("evidence-kind", "", "evidence kind: forged-attestation | double-mining | freshness-cheat (required)")
		evidenceFile = fs.String("evidence-file", "", "path to raw evidence bytes ('-' for stdin)")
		evidenceHex  = fs.String("evidence-hex", "", "hex-encoded evidence bytes")
		amount       = fs.Uint64("amount", 0, "proposed slash amount in dust (required, must be > 0)")
		memo         = fs.String("memo", "", "optional human-readable memo (≤256 bytes)")
		nonce        = fs.Uint64("nonce", 0, "submitter's account nonce")
		fee          = fs.Float64("fee", 0.001, "tx fee in CELL (must be > 0)")
		txID         = fs.String("id", "", "mempool tx id (default = random hex)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sender == "" || *nodeID == "" || *kind == "" || *amount == 0 {
		fs.Usage()
		return fmt.Errorf("--sender, --node-id, --evidence-kind, --amount are required (and amount > 0)")
	}
	evidence, err := readEvidenceBytes(*evidenceFile, *evidenceHex)
	if err != nil {
		return err
	}

	payload := slashing.SlashPayload{
		NodeID:          *nodeID,
		EvidenceKind:    slashing.EvidenceKind(*kind),
		EvidenceBlob:    evidence,
		SlashAmountDust: *amount,
		Memo:            *memo,
	}
	raw, err := slashing.EncodeSlashPayload(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	id := *txID
	if id == "" {
		id = generateTxID()
	}
	body, err := c.post("/mining/slash", envelope{
		"id":          id,
		"sender":      *sender,
		"nonce":       *nonce,
		"fee":         *fee,
		"contract_id": slashing.ContractID,
		"payload_b64": base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

// -----------------------------------------------------------------------------
// enrollment-status
// -----------------------------------------------------------------------------

// miningEnrollmentStatus handles `qsdmcli enrollment-status
// <node_id>`. Hits the GET /mining/enrollment/{node_id}
// read endpoint and pretty-prints the EnrollmentRecordView.
//
// Positional argument (not a flag) because there's exactly
// one required input — flags here would be ceremony.
func (c *CLI) miningEnrollmentStatus(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: qsdmcli enrollment-status <node-id>")
	}
	nodeID := args[0]
	if nodeID == "" || strings.Contains(nodeID, "/") {
		return fmt.Errorf("node-id must be non-empty and not contain '/'")
	}
	body, err := c.get("/mining/enrollment/" + url.PathEscape(nodeID))
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

// -----------------------------------------------------------------------------
// enrollments  (paginated list)
// -----------------------------------------------------------------------------

// miningEnrollmentsList handles `qsdmcli enrollments
// [--phase=...] [--limit=...] [--cursor=...] [--all]`.
// Pages over the on-chain enrollment registry via the
// GET /mining/enrollments endpoint.
//
// --all walks every page until HasMore is false, concatenating
// records into a single output. Useful for dashboards and
// dump scripts; without it, only the first page is returned.
//
// Cursor is passed through to the server unmodified, so a
// caller scripting an external pagination loop can request
// `qsdmcli enrollments --cursor=$prev_next_cursor`.
func (c *CLI) miningEnrollmentsList(args []string) error {
	fs := flag.NewFlagSet("enrollments", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		phase  = fs.String("phase", "", "phase filter: active | pending_unbond | revoked")
		limit  = fs.Int("limit", 0, "page size (0 = server default)")
		cursor = fs.String("cursor", "", "exclusive lower bound on node_id")
		all    = fs.Bool("all", false, "follow next_cursor until HasMore=false")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	buildPath := func(cur string) string {
		v := url.Values{}
		if *phase != "" {
			v.Set("phase", *phase)
		}
		if *limit > 0 {
			v.Set("limit", fmt.Sprintf("%d", *limit))
		}
		if cur != "" {
			v.Set("cursor", cur)
		}
		path := "/mining/enrollments"
		if encoded := v.Encode(); encoded != "" {
			path += "?" + encoded
		}
		return path
	}

	if !*all {
		body, err := c.get(buildPath(*cursor))
		if err != nil {
			return err
		}
		prettyPrint(body)
		return nil
	}

	// --all: stitch pages into a single aggregate envelope.
	type page struct {
		Records      []map[string]interface{} `json:"records"`
		NextCursor   string                   `json:"next_cursor"`
		HasMore      bool                     `json:"has_more"`
		TotalMatches uint64                   `json:"total_matches"`
		Phase        string                   `json:"phase,omitempty"`
	}
	type aggregate struct {
		Records      []map[string]interface{} `json:"records"`
		TotalMatches uint64                   `json:"total_matches"`
		Phase        string                   `json:"phase,omitempty"`
		PagesWalked  int                      `json:"pages_walked"`
	}
	agg := aggregate{Phase: *phase}
	cur := *cursor
	for i := 0; i < 10000; i++ { // hard upper bound; defends against server misbehaviour
		body, err := c.get(buildPath(cur))
		if err != nil {
			return err
		}
		var p page
		if err := json.Unmarshal(body, &p); err != nil {
			return fmt.Errorf("decode page: %w", err)
		}
		agg.Records = append(agg.Records, p.Records...)
		agg.TotalMatches = p.TotalMatches
		agg.PagesWalked++
		if !p.HasMore {
			break
		}
		cur = p.NextCursor
		if cur == "" {
			// Server bug: HasMore=true but empty next_cursor.
			// Surface as an error rather than spin forever.
			return fmt.Errorf("server returned has_more=true with empty next_cursor")
		}
	}
	out, err := json.MarshalIndent(agg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode aggregate: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// -----------------------------------------------------------------------------
// slash-receipt
// -----------------------------------------------------------------------------

// miningSlashReceipt handles `qsdmcli slash-receipt <tx-id>`.
// Hits the GET /mining/slash/{tx_id} read endpoint and
// pretty-prints the SlashReceiptView. The receipt captures
// whether the slash applied or rejected, the dust amounts on
// success, the reason tag on rejection, and the post-slash
// auto-revoke flag.
//
// Same positional-argument shape as enrollment-status: one
// required input, no flags.
//
// Operationally this is the answer to "did my slash work?".
// 200 means the chain processed the tx (inspect Outcome to
// see applied vs rejected); 404 means the tx_id is unknown
// or has been FIFO-evicted from the bounded receipt store
// (resubmit if you still have the evidence); 503 means the
// node has no v2 receipt store wired (point at a v2-aware
// peer).
func (c *CLI) miningSlashReceipt(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: qsdmcli slash-receipt <tx-id>")
	}
	txID := args[0]
	if txID == "" || strings.Contains(txID, "/") {
		return fmt.Errorf("tx-id must be non-empty and not contain '/'")
	}
	body, err := c.get("/mining/slash/" + url.PathEscape(txID))
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}
