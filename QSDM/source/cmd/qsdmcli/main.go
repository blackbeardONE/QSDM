package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultBaseURL = "http://localhost:8080/api/v1"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	baseURL := os.Getenv("QSDM_API_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	cli := &CLI{baseURL: baseURL, client: &http.Client{Timeout: 30 * time.Second}}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "status":
		err = cli.status()
	case "deploy":
		err = cli.deploy(args)
	case "execute":
		err = cli.execute(args)
	case "contracts":
		err = cli.listContracts()
	case "tx":
		err = cli.submitTx(args)
	case "receipt":
		err = cli.getReceipt(args)
	case "chain":
		err = cli.chainInfo()
	case "block":
		err = cli.getBlock(args)
	case "validators":
		err = cli.listValidators()
	case "bridge":
		err = cli.bridgeStatus()
	case "lock":
		err = cli.bridgeLock(args)
	case "tokens":
		err = cli.listTokens()
	case "mempool":
		err = cli.mempoolStats()
	case "audit":
		err = cli.auditSummary()
	case "health":
		err = cli.healthCheck()
	case "enroll":
		err = cli.miningEnroll(args)
	case "unenroll":
		err = cli.miningUnenroll(args)
	case "slash":
		err = cli.miningSlash(args)
	case "enrollment-status":
		err = cli.miningEnrollmentStatus(args)
	case "enrollments":
		err = cli.miningEnrollmentsList(args)
	case "slash-receipt":
		err = cli.miningSlashReceipt(args)
	case "slash-helper":
		err = cli.slashHelper(args)
	case "gov-helper":
		err = cli.govHelper(args)
	case "watch":
		err = cli.watchCommand(args)
	case "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// CLI wraps HTTP calls to the QSDM API.
type CLI struct {
	baseURL string
	token   string
	client  *http.Client
}

func (c *CLI) get(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", c.baseURL+path, nil)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *CLI) post(path string, payload interface{}) ([]byte, error) {
	data, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", c.baseURL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *CLI) status() error {
	body, err := c.get("/status")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) deploy(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: qsdmcli deploy <contract-id> <template: token|voting|escrow> [owner]")
	}
	payload := map[string]interface{}{
		"contract_id": args[0],
		"template":    args[1],
	}
	if len(args) > 2 {
		payload["owner"] = args[2]
	}
	body, err := c.post("/contracts/deploy", payload)
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) execute(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: qsdmcli execute <contract-id> <function> [key=value ...]")
	}
	params := make(map[string]interface{})
	for _, kv := range args[2:] {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			params[parts[0]] = parts[1]
		}
	}
	payload := map[string]interface{}{
		"contract_id": args[0],
		"function":    args[1],
		"args":        params,
	}
	body, err := c.post("/contracts/execute", payload)
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) listContracts() error {
	body, err := c.get("/contracts")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) submitTx(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: qsdmcli tx <sender> <recipient> <amount> [fee]")
	}
	payload := map[string]interface{}{
		"sender":    args[0],
		"recipient": args[1],
		"amount":    args[2],
	}
	if len(args) > 3 {
		payload["fee"] = args[3]
	}
	body, err := c.post("/transactions", payload)
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) getReceipt(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: qsdmcli receipt <tx-id>")
	}
	body, err := c.get("/receipts/" + args[0])
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) chainInfo() error {
	body, err := c.get("/chain")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) getBlock(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: qsdmcli block <height>")
	}
	body, err := c.get("/chain/blocks/" + args[0])
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) listValidators() error {
	body, err := c.get("/validators")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) bridgeStatus() error {
	body, err := c.get("/bridge/locks")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) bridgeLock(args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("usage: qsdmcli lock <source> <target> <asset> <amount> [recipient]")
	}
	payload := map[string]interface{}{
		"source_chain": args[0],
		"target_chain": args[1],
		"asset":        args[2],
		"amount":       args[3],
	}
	if len(args) > 4 {
		payload["recipient"] = args[4]
	}
	body, err := c.post("/bridge/lock", payload)
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) listTokens() error {
	body, err := c.get("/tokens")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) mempoolStats() error {
	body, err := c.get("/mempool/stats")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) auditSummary() error {
	body, err := c.get("/audit/summary")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func (c *CLI) healthCheck() error {
	body, err := c.get("/health")
	if err != nil {
		return err
	}
	prettyPrint(body)
	return nil
}

func prettyPrint(data []byte) {
	var obj interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		fmt.Println(string(data))
		return
	}
	pretty, _ := json.MarshalIndent(obj, "", "  ")
	fmt.Println(string(pretty))
}

func printUsage() {
	fmt.Println(`QSDM CLI — Command-line client for the QSDM blockchain

Usage: qsdmcli <command> [args...]

Commands:
  status                              Show node status
  health                              Health check
  chain                               Show chain info (height, tip)
  block <height>                      Get block by height
  validators                          List active validators
  mempool                             Show mempool statistics
  tx <sender> <recipient> <amount>    Submit a transaction
  receipt <tx-id>                     Get transaction receipt
  deploy <id> <template> [owner]      Deploy a smart contract
  execute <id> <func> [key=val ...]   Execute a contract function
  contracts                           List deployed contracts
  tokens                              List registered tokens
  bridge                              Show bridge locks
  lock <src> <dst> <asset> <amt>      Lock asset for cross-chain transfer
  audit                               Show audit checklist summary

v2 mining:
  enroll [flags]                      Enroll a NodeID with bonded stake
  unenroll [flags]                    Begin 7-day unbond on a NodeID
  slash [flags]                       Submit slashing evidence against a NodeID
  enrollment-status <node-id>         Query on-chain enrollment record
  enrollments [flags]                 Page over the on-chain enrollment registry
  slash-receipt <tx-id>               Query slash transaction outcome
  slash-helper <kind> [flags]         Build / inspect slashing evidence blobs offline
                                        kind ∈ {forged-attestation, double-mining, freshness-cheat, inspect}
  gov-helper <sub> [flags]            Build / inspect governance parameter-tuning payloads offline
                                        sub ∈ {propose-param, params, inspect}
  watch <subcommand> [flags]          Stream phase-change / stake-delta / slash-resolution events to stdout
                                        subcommand ∈ {enrollments, slashes}

  help                                Show this help

Environment:
  QSDM_API_URL    Base API URL (default: http://localhost:8080/api/v1)
  QSDM_TOKEN      Bearer token for authentication

v2 mining flags (enroll | unenroll | slash):
  enroll      --sender STR --node-id STR --gpu-uuid STR --hmac-key HEX
              [--stake DUST] [--nonce N] [--fee CELL] [--memo STR] [--id STR]
  unenroll    --sender STR --node-id STR
              [--reason STR] [--nonce N] [--fee CELL] [--id STR]
  slash       --sender STR --node-id STR --evidence-kind KIND --amount DUST
              (--evidence-file PATH | --evidence-hex HEX)
              [--memo STR] [--nonce N] [--fee CELL] [--id STR]

  KIND ∈ {forged-attestation, double-mining, freshness-cheat}
  '--evidence-file -' reads the evidence blob from stdin.

enrollments flags:
  --phase=PHASE   filter to active | pending_unbond | revoked
  --limit=N       page size (0 = server default; max 500)
  --cursor=ID     exclusive lower bound on node_id (empty starts at beginning)
  --all           follow next_cursor until exhausted; print one aggregate page

slash-helper subcommands (offline evidence-bundle assembly):
  forged-attestation --proof=PATH [--fault-class=KIND] [--memo=STR]
                     [--node-id=ID] [--out=PATH] [--print-cmd]
  double-mining      --proof-a=PATH --proof-b=PATH [--memo=STR]
                     [--node-id=ID] [--out=PATH] [--print-cmd]
  freshness-cheat    --proof=PATH --anchor-height=H --anchor-block-time=T
                     [--memo=STR] [--node-id=ID] [--out=PATH] [--print-cmd]
  inspect            --kind=KIND (--evidence-file=PATH | --evidence-hex=HEX)

  Use '-' for the path to read a proof / evidence blob from stdin.
  --print-cmd echoes a placeholder 'qsdmcli slash …' invocation to stderr
  after the evidence bytes are written, suitable for copy-paste into a script.

gov-helper subcommands (offline governance-payload assembly):
  propose-param   --param=NAME --value=N --effective-height=H
                  [--memo=STR] [--out=PATH] [--print-cmd]
  params          [--json]
  inspect         (--payload-file=PATH | --payload-hex=HEX)

  propose-param  builds a canonical chainparams.ParamSetPayload (qsdm/gov/v1)
                 and writes the encoded JSON to --out (default stdout). The
                 produced bytes are submitted via the operator's signed-tx
                 pipeline with ContractID=qsdm/gov/v1; chain-side acceptance
                 still requires the sender to be on the AuthorityList.
  params         lists the registered governance-tunable parameters with
                 their bounds, defaults and units. Source: chainparams.Registry.
  inspect        decodes a previously-built propose-param payload and pretty-
                 prints the structured view (kind, param, value, effective
                 height, memo, registry entry).

watch subcommands (operator surveillance, polling-only, no key required):
  enrollments [flags]                 Stream phase-change / stake-delta events
  slashes     [flags]                 Stream slash-receipt resolution events

  enrollments flags:
    --interval=DUR        polling cadence (default 30s, floor 5s)
    --phase=PHASE         server-side filter: active | pending_unbond | revoked
    --node-id=ID          single-node mode (mutually exclusive with --phase)
    --limit=N             list-mode page size (0 = server default)
    --once                emit one snapshot and exit (useful for cron)
    --json                JSON-Lines output (one event per line)
    --include-existing    on first poll, emit a synthetic 'new' event per record

  slashes flags:
    --tx-id=ID            slash tx_id to track (repeatable; merges with --tx-ids-file)
    --tx-ids-file=PATH    file with one tx_id per line ('-' = stdin); '#' starts a comment
    --interval=DUR        polling cadence (default 30s, floor 5s)
    --once                snapshot once and exit (useful for cron)
    --json                JSON-Lines output (one event per line)
    --include-pending     emit 'slash_pending' events on every cycle until tx resolves
    --exit-on-resolved    exit cleanly once every tracked tx has resolved

  Output (human, default):
    <RFC3339> <KIND> [node=<id>|tx=<id>] <kind-specific summary>
  Output (--json): one JSON object per line; the union of:
    enrollment fields {ts, event, node_id, phase, prev_phase, stake_dust,
      prev_stake_dust, delta_dust, slashable, enrolled_at_height,
      unbond_matures_at_height, revoked_at_height, error}
    slash fields      {ts, event, tx_id, outcome, prev_outcome, height,
      evidence_kind, slasher, node_id, slashed_dust, rewarded_dust,
      burned_dust, auto_revoked, auto_revoke_remaining_dust,
      reject_reason, error}
  All non-applicable fields are elided via omitempty so consumers can
  decode either stream with one struct definition.

  Event kinds (enrollments): new, transition, stake_delta, dropped, error
  Event kinds (slashes):     slash_resolved, slash_pending, slash_evicted,
                             slash_outcome_change, error

  Exits 0 on Ctrl-C / SIGTERM. Exits non-zero only on initial-snapshot
  failure; subsequent poll failures emit an 'error' event and continue.`)
}
