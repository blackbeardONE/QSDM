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
  help                                Show this help

Environment:
  QSDM_API_URL    Base API URL (default: http://localhost:8080/api/v1)
  QSDM_TOKEN      Bearer token for authentication`)
}
