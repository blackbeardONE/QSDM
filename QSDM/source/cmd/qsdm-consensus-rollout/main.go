package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const defaultHeadroomBlocks uint64 = 720

type nodeList []string

func (n *nodeList) String() string {
	return strings.Join(*n, ",")
}

func (n *nodeList) Set(value string) error {
	for _, raw := range strings.Split(value, ",") {
		node := strings.TrimSpace(raw)
		if node != "" {
			*n = append(*n, node)
		}
	}
	return nil
}

type options struct {
	nodes            nodeList
	headroomBlocks   uint64
	activationHeight uint64
	timeout          time.Duration
	jsonOutput       bool
}

type statusResponse struct {
	NodeID        string            `json:"node_id"`
	Version       string            `json:"version"`
	GitSHA        string            `json:"git_sha"`
	ChainTip      uint64            `json:"chain_tip"`
	Peers         int               `json:"peers"`
	ConsensusAuth consensusAuthInfo `json:"consensus_auth"`
}

type consensusAuthInfo struct {
	SignedConsensusSupported         bool   `json:"signed_consensus_supported"`
	RequireSignedVotes               bool   `json:"require_signed_votes"`
	SignedMessageActivationHeight    uint64 `json:"signed_message_activation_height"`
	SignedConsensusActive            bool   `json:"signed_consensus_active"`
	UnsignedConsensusTrafficAccepted bool   `json:"unsigned_consensus_traffic_accepted"`
}

type nodeReport struct {
	URL                              string `json:"url"`
	NodeID                           string `json:"node_id,omitempty"`
	Version                          string `json:"version,omitempty"`
	GitSHA                           string `json:"git_sha,omitempty"`
	ChainTip                         uint64 `json:"chain_tip"`
	Peers                            int    `json:"peers"`
	SignedConsensusSupported         bool   `json:"signed_consensus_supported"`
	RequireSignedVotes               bool   `json:"require_signed_votes"`
	SignedMessageActivationHeight    uint64 `json:"signed_message_activation_height"`
	SignedConsensusActive            bool   `json:"signed_consensus_active"`
	UnsignedConsensusTrafficAccepted bool   `json:"unsigned_consensus_traffic_accepted"`
}

type verdict struct {
	OK                        bool         `json:"ok"`
	State                     string       `json:"state"`
	Message                   string       `json:"message"`
	MaxChainTip               uint64       `json:"max_chain_tip"`
	SuggestedActivationHeight uint64       `json:"suggested_activation_height,omitempty"`
	Nodes                     []nodeReport `json:"nodes"`
	Warnings                  []string     `json:"warnings,omitempty"`
	Errors                    []string     `json:"errors,omitempty"`
}

func main() {
	opts := parseFlags(os.Args[1:])
	if len(opts.nodes) == 0 {
		fail(verdict{
			OK:      false,
			State:   "invalid_args",
			Message: "at least one --node URL is required",
			Errors:  []string{"usage: qsdm-consensus-rollout --node https://api.qsdm.tech/api/v1"},
		}, opts.jsonOutput)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	reports, err := fetchReports(ctx, opts.nodes)
	if err != nil {
		fail(verdict{
			OK:      false,
			State:   "fetch_failed",
			Message: "could not read every node status endpoint",
			Errors:  []string{err.Error()},
		}, opts.jsonOutput)
	}

	v := evaluate(reports, opts.headroomBlocks, opts.activationHeight)
	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
	} else {
		printHuman(v)
	}
	if !v.OK {
		os.Exit(1)
	}
}

func parseFlags(args []string) options {
	var opts options
	fs := flag.NewFlagSet("qsdm-consensus-rollout", flag.ExitOnError)
	fs.Var(&opts.nodes, "node", "QSDM node API base URL or /api/v1/status URL. Repeatable; comma-separated values also accepted.")
	fs.Uint64Var(&opts.headroomBlocks, "headroom-blocks", defaultHeadroomBlocks, "Blocks to add above the highest observed chain tip when suggesting an activation height.")
	fs.Uint64Var(&opts.activationHeight, "activation-height", 0, "Optional operator-chosen activation height to validate instead of only suggesting one.")
	fs.DurationVar(&opts.timeout, "timeout", 10*time.Second, "Total timeout for all status requests.")
	fs.BoolVar(&opts.jsonOutput, "json", false, "Emit machine-readable JSON.")
	_ = fs.Parse(args)
	return opts
}

func fetchReports(ctx context.Context, nodes []string) ([]nodeReport, error) {
	client := &http.Client{}
	var reports []nodeReport
	var errs []error
	for _, raw := range nodes {
		statusURL, err := statusEndpoint(raw)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", statusURL, err))
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		closeErr := resp.Body.Close()
		if readErr != nil {
			errs = append(errs, fmt.Errorf("%s: read response: %w", statusURL, readErr))
			continue
		}
		if closeErr != nil {
			errs = append(errs, fmt.Errorf("%s: close response: %w", statusURL, closeErr))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			errs = append(errs, fmt.Errorf("%s: HTTP %d: %s", statusURL, resp.StatusCode, strings.TrimSpace(string(body))))
			continue
		}
		var st statusResponse
		if err := json.Unmarshal(body, &st); err != nil {
			errs = append(errs, fmt.Errorf("%s: decode status: %w", statusURL, err))
			continue
		}
		reports = append(reports, reportFromStatus(statusURL, st))
	}
	return reports, errors.Join(errs...)
}

func statusEndpoint(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", errors.New("empty node URL")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%q: expected absolute http(s) URL", raw)
	}
	if strings.HasSuffix(u.Path, "/api/v1/status") {
		return u.String(), nil
	}
	if strings.HasSuffix(u.Path, "/api/v1") {
		u.Path += "/status"
		return u.String(), nil
	}
	u.Path += "/api/v1/status"
	return u.String(), nil
}

func reportFromStatus(statusURL string, st statusResponse) nodeReport {
	return nodeReport{
		URL:                              statusURL,
		NodeID:                           st.NodeID,
		Version:                          st.Version,
		GitSHA:                           st.GitSHA,
		ChainTip:                         st.ChainTip,
		Peers:                            st.Peers,
		SignedConsensusSupported:         st.ConsensusAuth.SignedConsensusSupported,
		RequireSignedVotes:               st.ConsensusAuth.RequireSignedVotes,
		SignedMessageActivationHeight:    st.ConsensusAuth.SignedMessageActivationHeight,
		SignedConsensusActive:            st.ConsensusAuth.SignedConsensusActive,
		UnsignedConsensusTrafficAccepted: st.ConsensusAuth.UnsignedConsensusTrafficAccepted,
	}
}

func evaluate(reports []nodeReport, headroomBlocks, activationHeight uint64) verdict {
	v := verdict{
		OK:      true,
		State:   "ready_to_schedule",
		Nodes:   append([]nodeReport(nil), reports...),
		Message: "all nodes support signed consensus and can schedule one shared future height",
	}
	sort.Slice(v.Nodes, func(i, j int) bool { return v.Nodes[i].URL < v.Nodes[j].URL })

	if len(reports) == 0 {
		v.OK = false
		v.State = "no_nodes"
		v.Message = "no node statuses were supplied"
		v.Errors = append(v.Errors, "provide at least one node status")
		return v
	}
	if headroomBlocks == 0 {
		headroomBlocks = defaultHeadroomBlocks
	}

	seenNodeIDs := map[string]string{}
	seenURLs := map[string]struct{}{}
	var requireTrue, requireFalse int
	var commonActivation *uint64
	minTip := ^uint64(0)
	for _, r := range reports {
		if _, ok := seenURLs[r.URL]; ok {
			v.Errors = append(v.Errors, fmt.Sprintf("duplicate status URL %s", r.URL))
		}
		seenURLs[r.URL] = struct{}{}
		if r.ChainTip > v.MaxChainTip {
			v.MaxChainTip = r.ChainTip
		}
		if r.ChainTip < minTip {
			minTip = r.ChainTip
		}
		if !r.SignedConsensusSupported {
			v.Errors = append(v.Errors, fmt.Sprintf("%s does not advertise signed_consensus_supported=true", r.URL))
		}
		if r.NodeID == "" {
			v.Warnings = append(v.Warnings, fmt.Sprintf("%s did not report a node_id; duplicate-validator detection is weakened", r.URL))
		} else if first, ok := seenNodeIDs[r.NodeID]; ok {
			v.Errors = append(v.Errors, fmt.Sprintf("duplicate node_id %s reported by %s and %s", r.NodeID, first, r.URL))
		} else {
			seenNodeIDs[r.NodeID] = r.URL
		}
		if r.Peers == 0 && len(reports) > 1 {
			v.Warnings = append(v.Warnings, fmt.Sprintf("%s reports peers=0; confirm it is really joined before activation", r.URL))
		}
		if r.RequireSignedVotes {
			requireTrue++
			if r.SignedMessageActivationHeight == 0 {
				v.Errors = append(v.Errors, fmt.Sprintf("%s requires signed votes but has activation height 0", r.URL))
			}
			if commonActivation == nil {
				a := r.SignedMessageActivationHeight
				commonActivation = &a
			} else if *commonActivation != r.SignedMessageActivationHeight {
				v.Errors = append(v.Errors, fmt.Sprintf("%s uses activation height %d; expected %d", r.URL, r.SignedMessageActivationHeight, *commonActivation))
			}
		} else {
			requireFalse++
			if r.SignedMessageActivationHeight != 0 {
				v.Errors = append(v.Errors, fmt.Sprintf("%s has activation height %d while require_signed_votes=false", r.URL, r.SignedMessageActivationHeight))
			}
			if r.SignedConsensusActive {
				v.Errors = append(v.Errors, fmt.Sprintf("%s reports signed consensus active while require_signed_votes=false", r.URL))
			}
		}
		if r.SignedConsensusActive && r.UnsignedConsensusTrafficAccepted {
			v.Errors = append(v.Errors, fmt.Sprintf("%s reports signed consensus active while still accepting unsigned traffic", r.URL))
		}
	}

	if requireTrue > 0 && requireFalse > 0 {
		v.Errors = append(v.Errors, "mixed rollout posture: some nodes require signed votes and some still accept unsigned votes")
	}

	switch {
	case requireTrue == len(reports):
		activation := uint64(0)
		if commonActivation != nil {
			activation = *commonActivation
		}
		v.SuggestedActivationHeight = activation
		if activationHeight != 0 && activationHeight != activation {
			v.Errors = append(v.Errors, fmt.Sprintf("requested activation height %d does not match deployed height %d", activationHeight, activation))
		}
		for _, r := range reports {
			if activation != 0 && r.ChainTip >= activation && (!r.SignedConsensusActive || r.UnsignedConsensusTrafficAccepted) {
				v.Errors = append(v.Errors, fmt.Sprintf("%s has crossed activation but does not report enforcement active", r.URL))
			}
		}
		if activation != 0 && minTip >= activation {
			v.State = "active"
			v.Message = "signed consensus enforcement is active on every checked node"
		} else {
			v.State = "scheduled"
			v.Message = "signed consensus enforcement is scheduled consistently on every checked node"
		}
	case requireFalse == len(reports):
		suggested := v.MaxChainTip + headroomBlocks
		if activationHeight != 0 {
			suggested = activationHeight
			if activationHeight <= v.MaxChainTip {
				v.Errors = append(v.Errors, fmt.Sprintf("activation height %d must be above current max chain tip %d", activationHeight, v.MaxChainTip))
			}
		}
		v.SuggestedActivationHeight = suggested
	default:
		v.Errors = append(v.Errors, "could not determine common signed-consensus posture")
	}

	if len(v.Errors) > 0 {
		v.OK = false
		v.State = "blocked"
		v.Message = "signed consensus enforcement is not safe to schedule from this snapshot"
	}
	return v
}

func printHuman(v verdict) {
	fmt.Println("QSDM signed-consensus rollout preflight")
	fmt.Printf("Verdict: %s\n", v.State)
	fmt.Println(v.Message)
	if v.MaxChainTip > 0 {
		fmt.Printf("Max chain tip: %d\n", v.MaxChainTip)
	}
	if v.SuggestedActivationHeight > 0 {
		fmt.Printf("Shared activation height: %d\n", v.SuggestedActivationHeight)
	}
	if len(v.Nodes) > 0 {
		fmt.Println()
		fmt.Println("Nodes:")
		for _, n := range v.Nodes {
			fmt.Printf("- %s tip=%d require_signed_votes=%t activation=%d active=%t unsigned_accepted=%t node_id=%s\n",
				n.URL,
				n.ChainTip,
				n.RequireSignedVotes,
				n.SignedMessageActivationHeight,
				n.SignedConsensusActive,
				n.UnsignedConsensusTrafficAccepted,
				shortID(n.NodeID),
			)
		}
	}
	if len(v.Warnings) > 0 {
		fmt.Println()
		fmt.Println("Warnings:")
		for _, w := range v.Warnings {
			fmt.Printf("- %s\n", w)
		}
	}
	if len(v.Errors) > 0 {
		fmt.Println()
		fmt.Println("Errors:")
		for _, e := range v.Errors {
			fmt.Printf("- %s\n", e)
		}
		return
	}
	if v.State == "ready_to_schedule" {
		fmt.Println()
		fmt.Println("Set the same values on every validator:")
		fmt.Println("QSDM_REQUIRE_SIGNED_VOTES=true")
		fmt.Printf("QSDM_SIGNED_MESSAGE_ACTIVATION_HEIGHT=%d\n", v.SuggestedActivationHeight)
	}
}

func fail(v verdict, jsonOutput bool) {
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
	} else {
		printHuman(v)
	}
	os.Exit(1)
}

func shortID(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:8] + "..." + id[len(id)-8:]
}
