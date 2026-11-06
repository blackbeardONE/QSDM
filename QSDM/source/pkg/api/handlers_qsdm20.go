package api

// handlers_qsdm20.go: QSDM-20 native tokens (QSDM/docs/docs/QSDM_20.md).
//
//	POST /api/v1/qsdm20/submit                   a signed token transaction
//	GET  /api/v1/qsdm20/tokens                   all tokens, newest first, with a cursor
//	GET  /api/v1/qsdm20/tokens/{id}              one token
//	GET  /api/v1/qsdm20/tokens/{id}/holders      its holders, largest balance first, with a cursor
//	GET  /api/v1/qsdm20/balances/{address}       every token an address holds
//
// The reads are public ledger data. The submit route is public too: the
// sender's ML-DSA-87 signature in the body is the authorization, and the
// node checks the transaction against its current state (activation height,
// signature, nonce, fee and token rules) before the mempool takes it.
//
// Pagination: limit is optional; without it every remaining item is
// returned. With it, a page ends with next_cursor, which the next request
// passes back as cursor. A cursor names the last item returned, so a page
// boundary does not move when items are added.

import (
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const (
	qsdm20TokenPrefix    = "/api/v1/qsdm20/tokens/"
	qsdm20BalancesPrefix = "/api/v1/qsdm20/balances/"
)

// QSDM20StateProvider is the read side of the token state.
// *chain.QSDM20StateStore satisfies it.
type QSDM20StateProvider interface {
	Tokens() []chain.QSDM20Token
	Token(id string) (chain.QSDM20Token, bool)
	Holders(id string) ([]chain.QSDM20Holder, bool)
	BalancesOf(address string) []chain.QSDM20Balance
	StateRoot() string
}

// QSDM20TxChecker reports whether a token transaction would apply in the
// next block on the current state. *chain.EnrollmentAwareApplier satisfies
// it.
type QSDM20TxChecker interface {
	CheckQSDM20Tx(tx *mempool.Tx) error
}

type qsdm20Wiring struct {
	mu       sync.RWMutex
	pool     MempoolSubmitter
	provider QSDM20StateProvider
	checker  QSDM20TxChecker
}

var qsdm20Global = &qsdm20Wiring{}

// SetQSDM20Mempool installs the mempool that takes submitted token
// transactions.
func SetQSDM20Mempool(pool MempoolSubmitter) {
	qsdm20Global.mu.Lock()
	defer qsdm20Global.mu.Unlock()
	qsdm20Global.pool = pool
}

// SetQSDM20StateProvider installs the consensus token state the reads serve.
func SetQSDM20StateProvider(provider QSDM20StateProvider) {
	qsdm20Global.mu.Lock()
	defer qsdm20Global.mu.Unlock()
	qsdm20Global.provider = provider
}

// SetQSDM20TxChecker installs the check a submitted transaction must pass.
func SetQSDM20TxChecker(checker QSDM20TxChecker) {
	qsdm20Global.mu.Lock()
	defer qsdm20Global.mu.Unlock()
	qsdm20Global.checker = checker
}

func currentQSDM20() (MempoolSubmitter, QSDM20StateProvider, QSDM20TxChecker) {
	qsdm20Global.mu.RLock()
	defer qsdm20Global.mu.RUnlock()
	return qsdm20Global.pool, qsdm20Global.provider, qsdm20Global.checker
}

// QSDM20SubmitRequest is the body of POST /api/v1/qsdm20/submit. signature
// is the sender's ML-DSA-87 signature over the signing digest of sender,
// nonce, fee_dust and payload (QSDM_20.md, "Signature"); public_key must
// hash (SHA-256) to sender. Both are lowercase hex.
type QSDM20SubmitRequest struct {
	Sender    string             `json:"sender"`
	Nonce     uint64             `json:"nonce"`
	FeeDust   uint64             `json:"fee_dust"`
	Payload   chain.QSDM20Action `json:"payload"`
	PublicKey string             `json:"public_key"`
	Signature string             `json:"signature"`
}

// QSDM20SubmitResponse acknowledges an admitted transaction. token is the
// token acted on; for a create, the id the new token will have.
type QSDM20SubmitResponse struct {
	TxID          string `json:"tx_id"`
	Action        string `json:"action"`
	Sender        string `json:"sender"`
	Nonce         uint64 `json:"nonce"`
	Token         string `json:"token"`
	Status        string `json:"status"`
	MempoolStatus string `json:"mempool_status"`
}

type QSDM20TokensResponse struct {
	Runtime    string              `json:"runtime"`
	Source     string              `json:"source"`
	StateRoot  string              `json:"state_root"`
	Tokens     []chain.QSDM20Token `json:"tokens"`
	NextCursor string              `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
}

type QSDM20TokenResponse struct {
	Runtime   string            `json:"runtime"`
	Source    string            `json:"source"`
	StateRoot string            `json:"state_root"`
	Token     chain.QSDM20Token `json:"token"`
}

type QSDM20HoldersResponse struct {
	Runtime    string               `json:"runtime"`
	Source     string               `json:"source"`
	StateRoot  string               `json:"state_root"`
	Token      string               `json:"token"`
	Holders    []chain.QSDM20Holder `json:"holders"`
	NextCursor string               `json:"next_cursor,omitempty"`
	HasMore    bool                 `json:"has_more"`
}

type QSDM20BalancesResponse struct {
	Runtime   string                `json:"runtime"`
	Source    string                `json:"source"`
	StateRoot string                `json:"state_root"`
	Address   string                `json:"address"`
	Balances  []chain.QSDM20Balance `json:"balances"`
}

// QSDM20TxFromRequest builds the consensus transaction for a submit body,
// exactly as a node does: the canonical JSON payload, the fee in CELL and
// the id bound to the signing digest.
func QSDM20TxFromRequest(req QSDM20SubmitRequest) (*mempool.Tx, error) {
	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, err
	}
	fee, err := chain.QSDM20FeeCell(req.FeeDust)
	if err != nil {
		return nil, err
	}
	digest := chain.QSDM20SigningDigest(req.Sender, req.Nonce, req.FeeDust, req.Payload)
	return &mempool.Tx{
		ID:         chain.QSDM20TxID(digest),
		Sender:     req.Sender,
		Fee:        fee,
		Nonce:      req.Nonce,
		Payload:    payload,
		ContractID: chain.QSDM20ContractID,
		Signature:  req.Signature,
		PublicKey:  req.PublicKey,
		AddedAt:    time.Now().UTC(),
	}, nil
}

// QSDM20SubmitHandler serves POST /api/v1/qsdm20/submit.
func (h *Handlers) QSDM20SubmitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	pool, _, checker := currentQSDM20()
	if pool == nil || checker == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "QSDM-20 submission is not configured")
		return
	}
	var req QSDM20SubmitRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid QSDM-20 transaction: "+SanitizeString(err.Error(), 256))
		return
	}
	if _, err := decoder.Token(); err != io.EOF {
		writeErrorResponse(w, http.StatusBadRequest, "invalid QSDM-20 transaction: trailing data after the JSON object")
		return
	}
	tx, err := QSDM20TxFromRequest(req)
	if err != nil {
		writeErrorResponse(w, http.StatusUnprocessableEntity, SanitizeString(err.Error(), 512))
		return
	}
	if err := checker.CheckQSDM20Tx(tx); err != nil {
		writeErrorResponse(w, http.StatusUnprocessableEntity, SanitizeString(err.Error(), 512))
		return
	}
	status := "submitted"
	if err := pool.Add(tx); err != nil {
		if errors.Is(err, mempool.ErrDuplicateTx) {
			status = "duplicate"
		} else {
			writeErrorResponse(w, http.StatusUnprocessableEntity, SanitizeString(err.Error(), 512))
			return
		}
	}
	token := req.Payload.Token
	if req.Payload.Action == chain.QSDM20ActionCreate {
		token = chain.QSDM20TokenID(req.Sender, req.Nonce)
	}
	writeJSONResponse(w, http.StatusOK, QSDM20SubmitResponse{
		TxID:          tx.ID,
		Action:        req.Payload.Action,
		Sender:        req.Sender,
		Nonce:         req.Nonce,
		Token:         token,
		Status:        "accepted",
		MempoolStatus: status,
	})
}

// QSDM20TokensHandler serves GET /api/v1/qsdm20/tokens: every token, newest
// first (creation height descending, then id ascending). The cursor is
// "<created_height>:<id>" of the last token of the previous page.
func (h *Handlers) QSDM20TokensHandler(w http.ResponseWriter, r *http.Request) {
	provider, ok := qsdm20ReadProvider(w, r)
	if !ok {
		return
	}
	limit, err := qsdm20Limit(r)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	tokens := provider.Tokens()
	start := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		height, id, ok := parseQSDM20TokensCursor(raw)
		if !ok {
			writeErrorResponse(w, http.StatusBadRequest, "cursor must be <created_height>:<token id> from next_cursor")
			return
		}
		start = sort.Search(len(tokens), func(i int) bool {
			t := tokens[i]
			return t.CreatedHeight < height || (t.CreatedHeight == height && t.ID > id)
		})
	}
	page, hasMore := qsdm20Page(len(tokens), start, limit)
	resp := QSDM20TokensResponse{
		Runtime:   "qsdm-native",
		Source:    "chain",
		StateRoot: provider.StateRoot(),
		Tokens:    append([]chain.QSDM20Token{}, tokens[start:page]...),
		HasMore:   hasMore,
	}
	if hasMore {
		last := tokens[page-1]
		resp.NextCursor = strconv.FormatUint(last.CreatedHeight, 10) + ":" + last.ID
	}
	writeJSONResponse(w, http.StatusOK, resp)
}

// QSDM20TokenRouteHandler serves GET /api/v1/qsdm20/tokens/{id} and
// GET /api/v1/qsdm20/tokens/{id}/holders. Holders come largest balance
// first, then by address; the cursor is "<balance>:<address>" of the last
// holder of the previous page.
func (h *Handlers) QSDM20TokenRouteHandler(w http.ResponseWriter, r *http.Request) {
	provider, ok := qsdm20ReadProvider(w, r)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, qsdm20TokenPrefix)
	id, sub, hasSub := strings.Cut(rest, "/")
	if hasSub && sub != "holders" {
		writeErrorResponse(w, http.StatusNotFound, "not found")
		return
	}
	if !qsdm20Hex64(id) {
		writeErrorResponse(w, http.StatusBadRequest, "token id must be 64 lowercase hex characters")
		return
	}
	if !hasSub {
		token, found := provider.Token(id)
		if !found {
			writeErrorResponse(w, http.StatusNotFound, "QSDM-20 token not found")
			return
		}
		writeJSONResponse(w, http.StatusOK, QSDM20TokenResponse{
			Runtime: "qsdm-native", Source: "chain", StateRoot: provider.StateRoot(), Token: token,
		})
		return
	}
	limit, err := qsdm20Limit(r)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	holders, found := provider.Holders(id)
	if !found {
		writeErrorResponse(w, http.StatusNotFound, "QSDM-20 token not found")
		return
	}
	start := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		balance, address, ok := parseQSDM20HoldersCursor(raw)
		if !ok {
			writeErrorResponse(w, http.StatusBadRequest, "cursor must be <balance>:<address> from next_cursor")
			return
		}
		start = sort.Search(len(holders), func(i int) bool {
			b, _ := new(big.Int).SetString(holders[i].Balance, 10)
			if c := b.Cmp(balance); c != 0 {
				return c < 0
			}
			return holders[i].Address > address
		})
	}
	page, hasMore := qsdm20Page(len(holders), start, limit)
	resp := QSDM20HoldersResponse{
		Runtime:   "qsdm-native",
		Source:    "chain",
		StateRoot: provider.StateRoot(),
		Token:     id,
		Holders:   append([]chain.QSDM20Holder{}, holders[start:page]...),
		HasMore:   hasMore,
	}
	if hasMore {
		last := holders[page-1]
		resp.NextCursor = last.Balance + ":" + last.Address
	}
	writeJSONResponse(w, http.StatusOK, resp)
}

// QSDM20BalancesHandler serves GET /api/v1/qsdm20/balances/{address}: every
// token the address holds, by token id.
func (h *Handlers) QSDM20BalancesHandler(w http.ResponseWriter, r *http.Request) {
	provider, ok := qsdm20ReadProvider(w, r)
	if !ok {
		return
	}
	address := strings.TrimPrefix(r.URL.Path, qsdm20BalancesPrefix)
	if !qsdm20Hex64(address) {
		writeErrorResponse(w, http.StatusBadRequest, "address must be 64 lowercase hex characters")
		return
	}
	balances := provider.BalancesOf(address)
	if balances == nil {
		balances = []chain.QSDM20Balance{}
	}
	writeJSONResponse(w, http.StatusOK, QSDM20BalancesResponse{
		Runtime: "qsdm-native", Source: "chain", StateRoot: provider.StateRoot(),
		Address: address, Balances: balances,
	})
}

func qsdm20ReadProvider(w http.ResponseWriter, r *http.Request) (QSDM20StateProvider, bool) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return nil, false
	}
	_, provider, _ := currentQSDM20()
	if provider == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "QSDM-20 token state is not configured")
		return nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return provider, true
}

// qsdm20Limit reads the optional page size: absent means every remaining
// item; present, it must be a positive integer.
func qsdm20Limit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, errors.New("limit must be a positive integer")
	}
	return n, nil
}

// qsdm20Page returns the end of the page that starts at start, and whether
// items remain after it.
func qsdm20Page(total, start, limit int) (end int, hasMore bool) {
	if limit == 0 || total-start <= limit {
		return total, false
	}
	return start + limit, true
}

func parseQSDM20TokensCursor(raw string) (uint64, string, bool) {
	h, id, ok := strings.Cut(raw, ":")
	if !ok || !qsdm20Hex64(id) {
		return 0, "", false
	}
	height, err := strconv.ParseUint(h, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return height, id, true
}

func parseQSDM20HoldersCursor(raw string) (*big.Int, string, bool) {
	b, address, ok := strings.Cut(raw, ":")
	if !ok || !qsdm20Hex64(address) || b == "" || strings.Trim(b, "0123456789") != "" {
		return nil, "", false
	}
	balance, ok := new(big.Int).SetString(b, 10)
	if !ok {
		return nil, "", false
	}
	return balance, address, true
}

// qsdm20Hex64 accepts a token id or a QSDM address: 64 lowercase hex
// characters.
func qsdm20Hex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
