package api

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const qsdmTaskActionLogPathEnv = "QSDM_TASK_ACTION_LOG_PATH"

var qsdmTaskActionLogMu sync.Mutex

type qsdmTaskActionSubmitterHolder struct {
	mu   sync.RWMutex
	pool MempoolSubmitter
}

var qsdmTaskActionMempoolHolder = &qsdmTaskActionSubmitterHolder{}

// SetTaskActionMempool installs (or removes, when pool==nil)
// the process-wide MempoolSubmitter used by signed task-action
// submission. Validators call this at startup after constructing
// the live mempool.
func SetTaskActionMempool(pool MempoolSubmitter) {
	qsdmTaskActionMempoolHolder.mu.Lock()
	defer qsdmTaskActionMempoolHolder.mu.Unlock()
	qsdmTaskActionMempoolHolder.pool = pool
}

func currentTaskActionMempool() MempoolSubmitter {
	qsdmTaskActionMempoolHolder.mu.RLock()
	defer qsdmTaskActionMempoolHolder.mu.RUnlock()
	return qsdmTaskActionMempoolHolder.pool
}

// TaskActionMempoolReady reports whether signed task actions can be submitted
// to the live validator mempool. Validators use this during startup so a
// partially wired task-action API cannot silently return 503 in production.
func TaskActionMempoolReady() bool {
	return currentTaskActionMempool() != nil
}

// TaskActionSubmissionReady reports whether the public signed-task endpoint
// has both of its process-wide dependencies: a live mempool and durable action
// logging. It is safe to expose through the public status response.
func TaskActionSubmissionReady() bool {
	return TaskActionMempoolReady() && qsdmTaskActionLogPath() != ""
}

type QSDMTaskActionEnvelope struct {
	ID        string  `json:"id"`
	Sender    string  `json:"sender"`
	TaskID    string  `json:"task_id"`
	Action    string  `json:"action"`
	Amount    float64 `json:"amount,omitempty"`
	Payload   string  `json:"payload,omitempty"`
	Nonce     uint64  `json:"nonce,omitempty"`
	Timestamp string  `json:"timestamp"`
	Signature string  `json:"signature"`
	PublicKey string  `json:"public_key,omitempty"`
}

type QSDMTaskActionRecord struct {
	ReceivedAt string                 `json:"received_at"`
	Envelope   QSDMTaskActionEnvelope `json:"envelope"`
}

type QSDMTaskActionSubmitResponse struct {
	ActionID         string `json:"action_id"`
	Status           string `json:"status"`
	Sender           string `json:"sender"`
	TaskID           string `json:"task_id"`
	Action           string `json:"action"`
	LastNonce        uint64 `json:"last_nonce,omitempty"`
	MempoolSubmitted bool   `json:"mempool_submitted"`
	MempoolStatus    string `json:"mempool_status,omitempty"`
	MempoolError     string `json:"mempool_error,omitempty"`
}

type QSDMTaskActionsListResponse struct {
	Configured bool                   `json:"configured"`
	Source     string                 `json:"source,omitempty"`
	Actions    []QSDMTaskActionRecord `json:"actions"`
}

func qsdmTaskActionLogPath() string {
	return strings.TrimSpace(os.Getenv(qsdmTaskActionLogPathEnv))
}

func validateQSDMTaskActionEnvelope(env QSDMTaskActionEnvelope) error {
	if err := ValidateTransactionID(env.ID); err != nil {
		return fmt.Errorf("invalid action id: %w", err)
	}
	if err := ValidateAddress(env.Sender); err != nil {
		return fmt.Errorf("invalid sender address: %w", err)
	}
	if err := ValidateString(env.TaskID, "task_id", 1, 256); err != nil {
		return err
	}
	switch env.Action {
	case "start", "stop", "stake", "fund", "unstake", "submit", "claim", "withdraw", "migrate",
		chain.TaskActionCatalogRegister, chain.TaskActionCatalogUpdate,
		chain.TaskActionCatalogPause, chain.TaskActionCatalogResume:
	default:
		return fmt.Errorf("unsupported action %q", SanitizeString(env.Action, 64))
	}
	if env.Amount < 0 {
		return fmt.Errorf("amount cannot be negative")
	}
	if env.Amount > 0 {
		if err := ValidateAmount(env.Amount); err != nil {
			return err
		}
	}
	if err := ValidateString(env.Payload, "payload", 0, 100000); err != nil {
		return err
	}
	if err := ValidateTimestamp(env.Timestamp); err != nil {
		return err
	}
	if env.PublicKey == "" {
		return fmt.Errorf("envelope.public_key is required")
	}
	if env.Signature == "" {
		return fmt.Errorf("envelope.signature is required")
	}
	return nil
}

func (h *Handlers) verifyQSDMTaskActionEnvelope(env QSDMTaskActionEnvelope) error {
	if h.walletService == nil {
		return fmt.Errorf(msgWalletServiceUnavailable)
	}
	pubBytes, err := hex.DecodeString(env.PublicKey)
	if err != nil {
		return fmt.Errorf("envelope.public_key is not valid hex")
	}
	derivedAddr := hex.EncodeToString(sha256Sum(pubBytes))
	if derivedAddr != env.Sender {
		return fmt.Errorf("envelope.sender does not match hex(sha256(public_key))")
	}

	sigBytes, err := hex.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("envelope.signature is not valid hex")
	}

	unsigned := env
	unsigned.Signature = ""
	unsigned.PublicKey = ""
	canonical, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("failed to canonicalise envelope")
	}
	ok, verr := h.walletService.VerifySignature(canonical, sigBytes, pubBytes)
	if verr != nil || !ok {
		return fmt.Errorf("signature does not verify under envelope.public_key")
	}
	return nil
}

func qsdmTaskActionExists(path, actionID string) (bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var record QSDMTaskActionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err == nil && record.Envelope.ID == actionID {
			return true, nil
		}
	}
	return false, scanner.Err()
}

type qsdmTaskActionAppendResult struct {
	Duplicate   bool
	NonceReplay bool
	LastNonce   uint64
}

func inspectQSDMTaskActionLog(path string, env QSDMTaskActionEnvelope) (qsdmTaskActionAppendResult, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return qsdmTaskActionAppendResult{}, nil
	}
	if err != nil {
		return qsdmTaskActionAppendResult{}, err
	}
	defer file.Close()

	var result qsdmTaskActionAppendResult
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var record QSDMTaskActionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if record.Envelope.ID == env.ID {
			result.Duplicate = true
			return result, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return qsdmTaskActionAppendResult{}, err
	}
	return result, nil
}

func appendQSDMTaskAction(path string, record QSDMTaskActionRecord) (qsdmTaskActionAppendResult, error) {
	qsdmTaskActionLogMu.Lock()
	defer qsdmTaskActionLogMu.Unlock()

	result, err := inspectQSDMTaskActionLog(path, record.Envelope)
	if err != nil {
		return qsdmTaskActionAppendResult{}, err
	}
	if result.Duplicate || result.NonceReplay {
		return result, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return qsdmTaskActionAppendResult{}, err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return qsdmTaskActionAppendResult{}, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return qsdmTaskActionAppendResult{}, err
	}
	defer file.Close()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return qsdmTaskActionAppendResult{}, err
	}
	return result, nil
}

func readQSDMTaskActions(path, taskID, sender string, limit int) ([]QSDMTaskActionRecord, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return []QSDMTaskActionRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	unlimited := limit < 0
	if !unlimited && limit <= 0 {
		limit = 100
	}
	if !unlimited && limit > 500 {
		limit = 500
	}

	actions := []QSDMTaskActionRecord{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var record QSDMTaskActionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if taskID != "" && record.Envelope.TaskID != taskID {
			continue
		}
		if sender != "" && record.Envelope.Sender != sender {
			continue
		}
		actions = append(actions, record)
		if !unlimited && len(actions) >= limit {
			break
		}
	}
	return actions, scanner.Err()
}

func (h *Handlers) QSDMTaskActionSubmitSignedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := qsdmTaskActionLogPath()
	if path == "" {
		writeErrorResponse(w, http.StatusServiceUnavailable, qsdmTaskActionLogPathEnv+" is not configured")
		return
	}
	if h.walletService == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, msgWalletServiceUnavailable)
		return
	}

	var env QSDMTaskActionEnvelope
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&env); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid envelope: "+err.Error())
		return
	}
	env.Action = strings.ToLower(strings.TrimSpace(env.Action))
	env.Sender = strings.TrimSpace(strings.ToLower(env.Sender))
	env.TaskID = strings.TrimSpace(env.TaskID)

	if err := validateQSDMTaskActionEnvelope(env); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.verifyQSDMTaskActionEnvelope(env); err != nil {
		if strings.Contains(err.Error(), "sender does not match") {
			writeErrorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		writeErrorResponse(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	preflightResult, err := inspectQSDMTaskActionLog(path, env)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to inspect task action log: "+err.Error())
		return
	}
	if preflightResult.Duplicate {
		writeJSONResponse(w, http.StatusConflict, QSDMTaskActionSubmitResponse{
			ActionID: env.ID,
			Status:   "duplicate",
			Sender:   env.Sender,
			TaskID:   env.TaskID,
			Action:   env.Action,
		})
		return
	}

	mempoolSubmitted, mempoolStatus, mempoolError := submitQSDMTaskActionToMempool(env)
	if !mempoolSubmitted {
		statusCode := http.StatusUnprocessableEntity
		if mempoolStatus == "not_configured" {
			statusCode = http.StatusServiceUnavailable
		}
		writeJSONResponse(w, statusCode, QSDMTaskActionSubmitResponse{
			ActionID:         env.ID,
			Status:           "rejected",
			Sender:           env.Sender,
			TaskID:           env.TaskID,
			Action:           env.Action,
			MempoolSubmitted: false,
			MempoolStatus:    mempoolStatus,
			MempoolError:     mempoolError,
		})
		return
	}

	record := QSDMTaskActionRecord{
		ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Envelope:   env,
	}
	appendResult, err := appendQSDMTaskAction(path, record)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to persist task action: "+err.Error())
		return
	}
	if appendResult.Duplicate {
		writeJSONResponse(w, http.StatusConflict, QSDMTaskActionSubmitResponse{
			ActionID: env.ID,
			Status:   "duplicate",
			Sender:   env.Sender,
			TaskID:   env.TaskID,
			Action:   env.Action,
		})
		return
	}

	writeJSONResponse(w, http.StatusOK, QSDMTaskActionSubmitResponse{
		ActionID:         env.ID,
		Status:           "accepted",
		Sender:           env.Sender,
		TaskID:           env.TaskID,
		Action:           env.Action,
		MempoolSubmitted: mempoolSubmitted,
		MempoolStatus:    mempoolStatus,
		MempoolError:     mempoolError,
	})
}

func submitQSDMTaskActionToMempool(env QSDMTaskActionEnvelope) (bool, string, string) {
	pool := currentTaskActionMempool()
	if pool == nil {
		return false, "not_configured", ""
	}
	tx, err := qsdmTaskActionMempoolTx(env)
	if err != nil {
		return false, "encode_failed", SanitizeString(err.Error(), 256)
	}
	if err := pool.Add(tx); err != nil {
		if errors.Is(err, mempool.ErrDuplicateTx) {
			return true, "duplicate", ""
		}
		return false, "rejected", SanitizeString(err.Error(), 256)
	}
	return true, "submitted", ""
}

func qsdmTaskActionMempoolTx(env QSDMTaskActionEnvelope) (*mempool.Tx, error) {
	payload, err := json.Marshal(qsdmTaskActionToChain(env))
	if err != nil {
		return nil, err
	}
	amount := 0.0
	if env.Action == "stake" || env.Action == "fund" {
		amount = env.Amount
	}
	return &mempool.Tx{
		ID:         env.ID,
		Sender:     env.Sender,
		Amount:     amount,
		Nonce:      env.Nonce,
		Payload:    payload,
		ContractID: chain.TaskContractID,
	}, nil
}

func qsdmTaskActionToChain(env QSDMTaskActionEnvelope) chain.TaskAction {
	return chain.TaskAction{
		ID:        env.ID,
		Sender:    env.Sender,
		TaskID:    env.TaskID,
		Action:    env.Action,
		Amount:    env.Amount,
		Payload:   env.Payload,
		Nonce:     env.Nonce,
		Timestamp: env.Timestamp,
	}
}

func isIgnorableTaskActionProjectionError(err error) bool {
	return errors.Is(err, chain.ErrDuplicateTaskAction) ||
		errors.Is(err, chain.ErrTaskActionNonceReplay) ||
		errors.Is(err, chain.ErrTaskActionRequiresStake) ||
		errors.Is(err, chain.ErrInsufficientTaskRewardPool) ||
		// The operator log predates block-height-aware replay and can contain
		// valid historical CPU proofs without a resource discriminator. The
		// canonical chain provider already contains those actions; ignore only
		// this typed legacy shape while building the read-only API projection.
		errors.Is(err, chain.ErrLegacyResourceWorkerProof)
}

func (h *Handlers) QSDMTaskActionsListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := qsdmTaskActionLogPath()
	if path == "" {
		writeJSONResponse(w, http.StatusOK, QSDMTaskActionsListResponse{
			Configured: false,
			Actions:    []QSDMTaskActionRecord{},
		})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	actions, err := readQSDMTaskActions(
		path,
		strings.TrimSpace(r.URL.Query().Get("task_id")),
		strings.TrimSpace(strings.ToLower(r.URL.Query().Get("sender"))),
		limit,
	)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to read task action log: "+err.Error())
		return
	}
	writeJSONResponse(w, http.StatusOK, QSDMTaskActionsListResponse{
		Configured: true,
		Source:     path,
		Actions:    actions,
	})
}

func (h *Handlers) QSDMTaskActionRouteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	actionID := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/actions/")
	actionID = strings.TrimSpace(strings.Trim(actionID, "/"))
	if actionID == "" || actionID == "submit-signed" {
		writeErrorResponse(w, http.StatusBadRequest, "action_id required")
		return
	}

	path := qsdmTaskActionLogPath()
	if path == "" {
		writeErrorResponse(w, http.StatusServiceUnavailable, qsdmTaskActionLogPathEnv+" is not configured")
		return
	}

	actions, err := readQSDMTaskActions(path, "", "", 500)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to read task action log: "+err.Error())
		return
	}
	for _, action := range actions {
		if action.Envelope.ID == actionID {
			writeJSONResponse(w, http.StatusOK, action)
			return
		}
	}
	writeErrorResponse(w, http.StatusNotFound, "task action not found")
}
