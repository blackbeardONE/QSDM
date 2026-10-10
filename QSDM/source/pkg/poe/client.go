package poe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client-side helper: the parent_cells a wallet should sign.
//
// Wallets call FetchParents just before building a transfer, put the result
// in parent_cells, then sign. The parents are the newest transactions the
// node has committed, which satisfy the PoE rules both before and after the
// activation height.

// parentsResponse is the subset of GET /api/v1/chain/parents a wallet needs.
type parentsResponse struct {
	Parents []string `json:"parents"`
}

// receiptsResponse is the subset of GET /api/v1/receipts used as a fallback
// on nodes released before /api/v1/chain/parents existed.
type receiptsResponse struct {
	Receipts []struct {
		TxID   string `json:"tx_id"`
		Status int    `json:"status"`
	} `json:"receipts"`
}

// ErrNoParents is returned when the node offered no usable parents.
var ErrNoParents = errors.New("poe: node offered no usable parents")

// APIRoot normalises a node URL to the root under which /api/v1 lives. It
// accepts "https://api.qsdm.tech", ".../api/v1" and trailing slashes.
func APIRoot(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	return strings.TrimSuffix(base, "/api/v1")
}

// FetchParents returns MinParents committed transaction IDs from the node at
// base. It asks GET /api/v1/chain/parents first; if that route is missing
// (404/405/501 on an older node) it falls back to the newest successful
// receipts from GET /api/v1/receipts. Every returned ID is a well-formed,
// distinct parent reference. A nil client uses http.DefaultClient.
func FetchParents(ctx context.Context, client *http.Client, base string) ([]string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	root := APIRoot(base)
	var parents parentsResponse
	status, err := getJSON(ctx, client, root+"/api/v1/chain/parents", &parents)
	if err == nil {
		return pickParents(parents.Parents)
	}
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed && status != http.StatusNotImplemented {
		return nil, err
	}
	var receipts receiptsResponse
	if _, err := getJSON(ctx, client, root+"/api/v1/receipts?limit=32", &receipts); err != nil {
		return nil, fmt.Errorf("poe: parents endpoint unavailable and receipts fallback failed: %w", err)
	}
	ids := make([]string, 0, len(receipts.Receipts))
	for _, r := range receipts.Receipts {
		if r.Status == 1 { // chain.ReceiptSuccess: the transaction is in a block
			ids = append(ids, r.TxID)
		}
	}
	return pickParents(ids)
}

// pickParents keeps the first MinParents distinct, well-formed IDs.
func pickParents(ids []string) ([]string, error) {
	out := make([]string, 0, MinParents)
	seen := make(map[string]struct{}, MinParents)
	for _, id := range ids {
		if !ValidParentID(id) {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) == MinParents {
			return out, nil
		}
	}
	return nil, ErrNoParents
}

func getJSON(ctx context.Context, client *http.Client, url string, into interface{}) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return resp.StatusCode, fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into); err != nil {
		return resp.StatusCode, fmt.Errorf("GET %s: decode: %w", url, err)
	}
	return resp.StatusCode, nil
}
