package edgepool

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

const (
	MotherContextVersion  = 1
	MotherTokenDomain     = "QSDM-EDGE-LOCAL-MOTHER-TOKEN-v1"
	LegacyMotherID        = "legacy"
	motherTenantStateFile = "mother-tenants.json"
	motherTenantStateV1   = 1
	maximumActiveMothers  = 128
	maximumMotherRecords  = 1024
)

var (
	motherIDPattern          = regexp.MustCompile(`^mother-[0-9a-f]{24}$`)
	federatedMotherIDPattern = regexp.MustCompile(`^federation-[0-9a-f]{24}$`)
	motherNamePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()-]{0,63}$`)
	motherStateMu            sync.Mutex
)

// MotherContext scopes one permanent local-network credential to one named
// QSDM Hive. The context is sent with every request; its derived token proves
// that the Relay created it and prevents one Hive from becoming another.
type MotherContext struct {
	Version    int    `json:"version"`
	MotherID   string `json:"mother_id"`
	MotherName string `json:"mother_name"`
	IssuedAt   string `json:"issued_at"`
}

// MotherTenantStatus is safe to display in the local Relay controller. It
// never contains the Relay master token or the Hive's derived credential.
type MotherTenantStatus struct {
	MotherID   string `json:"mother_id"`
	MotherName string `json:"mother_name"`
	IssuedAt   string `json:"issued_at"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
	RevokedAt  string `json:"revoked_at,omitempty"`
}

type motherTenantRecord struct {
	MotherTenantStatus
	ContextHash string `json:"context_hash"`
}

type motherTenantState struct {
	Version int                  `json:"version"`
	Tenants []motherTenantRecord `json:"tenants"`
}

func normalizeMotherContext(value MotherContext) (MotherContext, error) {
	if value.Version != MotherContextVersion {
		return MotherContext{}, fmt.Errorf("Mother Hive context version must be %d", MotherContextVersion)
	}
	value.MotherID = strings.ToLower(strings.TrimSpace(value.MotherID))
	if !motherIDPattern.MatchString(value.MotherID) {
		return MotherContext{}, errors.New("Mother Hive id is invalid")
	}
	value.MotherName = strings.TrimSpace(value.MotherName)
	if !motherNamePattern.MatchString(value.MotherName) {
		return MotherContext{}, errors.New("Mother Hive name must contain 1-64 ordinary letters, numbers, spaces, dots, underscores, parentheses, or hyphens")
	}
	issuedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(value.IssuedAt))
	if err != nil || issuedAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return MotherContext{}, errors.New("Mother Hive issue time is invalid")
	}
	value.IssuedAt = issuedAt.UTC().Format(time.RFC3339)
	return value, nil
}

func EncodeMotherContext(value MotherContext) (string, MotherContext, error) {
	normalized, err := normalizeMotherContext(value)
	if err != nil {
		return "", MotherContext{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", MotherContext{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw), normalized, nil
}

func DecodeMotherContext(encoded string) (MotherContext, error) {
	if len(encoded) == 0 || len(encoded) > 2048 {
		return MotherContext{}, errors.New("Mother Hive context is missing or too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return MotherContext{}, errors.New("Mother Hive context is damaged")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value MotherContext
	if err := decoder.Decode(&value); err != nil {
		return MotherContext{}, errors.New("Mother Hive context is damaged")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return MotherContext{}, errors.New("Mother Hive context contains trailing data")
	}
	normalized, err := normalizeMotherContext(value)
	if err != nil {
		return MotherContext{}, err
	}
	canonical, _, err := EncodeMotherContext(normalized)
	if err != nil || !hmac.Equal([]byte(canonical), []byte(encoded)) {
		return MotherContext{}, errors.New("Mother Hive context is not canonical")
	}
	return normalized, nil
}

func DeriveMotherToken(motherToken []byte, encodedContext string) ([]byte, MotherContext, error) {
	if len(motherToken) < 32 {
		return nil, MotherContext{}, errors.New("Relay Mother Hive token must contain at least 32 bytes")
	}
	contextValue, err := DecodeMotherContext(encodedContext)
	if err != nil {
		return nil, MotherContext{}, err
	}
	mac := hmac.New(sha256.New, motherToken)
	_, _ = mac.Write([]byte(MotherTokenDomain))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(encodedContext))
	return mac.Sum(nil), contextValue, nil
}

func motherTenantPath(stateDir string) string {
	return filepath.Join(stateDir, motherTenantStateFile)
}

func loadMotherTenantState(stateDir string) (motherTenantState, error) {
	state := motherTenantState{Version: motherTenantStateV1, Tenants: []motherTenantRecord{}}
	raw, err := os.ReadFile(motherTenantPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return motherTenantState{}, fmt.Errorf("read Mother Hive registry: %w", err)
	}
	if err := json.Unmarshal(raw, &state); err != nil || state.Version != motherTenantStateV1 || len(state.Tenants) > maximumMotherRecords {
		return motherTenantState{}, errors.New("Mother Hive registry is invalid")
	}
	seen := make(map[string]struct{}, len(state.Tenants))
	for _, tenant := range state.Tenants {
		if !motherIDPattern.MatchString(tenant.MotherID) || !motherNamePattern.MatchString(tenant.MotherName) {
			return motherTenantState{}, errors.New("Mother Hive registry contains an invalid identity")
		}
		if decoded, err := hex.DecodeString(tenant.ContextHash); err != nil || len(decoded) != sha256.Size {
			return motherTenantState{}, errors.New("Mother Hive registry contains an invalid context hash")
		}
		if _, duplicate := seen[tenant.MotherID]; duplicate {
			return motherTenantState{}, errors.New("Mother Hive registry contains a duplicate identity")
		}
		seen[tenant.MotherID] = struct{}{}
	}
	return state, nil
}

func saveMotherTenantState(stateDir string, state motherTenantState) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create Relay state directory: %w", err)
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(motherTenantPath(stateDir), append(raw, '\n'), 0o600)
}

// CreateMotherTenantCredential registers a named local Mother Hive and
// returns the context plus its scoped token. The Relay master token is never
// copied into the pairing code.
func CreateMotherTenantCredential(stateDir string, motherToken []byte, name string, now time.Time) (string, []byte, MotherTenantStatus, error) {
	name = strings.TrimSpace(name)
	if !motherNamePattern.MatchString(name) {
		return "", nil, MotherTenantStatus{}, errors.New("Mother Hive name must contain 1-64 ordinary letters, numbers, spaces, dots, underscores, parentheses, or hyphens")
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, MotherTenantStatus{}, fmt.Errorf("generate Mother Hive id: %w", err)
	}
	contextValue := MotherContext{
		Version:    MotherContextVersion,
		MotherID:   "mother-" + hex.EncodeToString(idBytes),
		MotherName: name,
		IssuedAt:   now.UTC().Format(time.RFC3339),
	}
	encodedContext, normalized, err := EncodeMotherContext(contextValue)
	if err != nil {
		return "", nil, MotherTenantStatus{}, err
	}
	token, _, err := DeriveMotherToken(motherToken, encodedContext)
	if err != nil {
		return "", nil, MotherTenantStatus{}, err
	}
	digest := sha256.Sum256([]byte(encodedContext))
	record := motherTenantRecord{
		MotherTenantStatus: MotherTenantStatus{
			MotherID:   normalized.MotherID,
			MotherName: normalized.MotherName,
			IssuedAt:   normalized.IssuedAt,
		},
		ContextHash: hex.EncodeToString(digest[:]),
	}

	motherStateMu.Lock()
	defer motherStateMu.Unlock()
	state, err := loadMotherTenantState(stateDir)
	if err != nil {
		return "", nil, MotherTenantStatus{}, err
	}
	active := 0
	for _, tenant := range state.Tenants {
		if tenant.RevokedAt == "" {
			active++
		}
	}
	if active >= maximumActiveMothers {
		return "", nil, MotherTenantStatus{}, fmt.Errorf("Relay already has the maximum of %d active Mother Hive identities", maximumActiveMothers)
	}
	if len(state.Tenants) >= maximumMotherRecords {
		return "", nil, MotherTenantStatus{}, fmt.Errorf("Relay Mother Hive audit history reached its limit of %d identities", maximumMotherRecords)
	}
	state.Tenants = append(state.Tenants, record)
	sort.Slice(state.Tenants, func(i, j int) bool { return state.Tenants[i].MotherID < state.Tenants[j].MotherID })
	if err := saveMotherTenantState(stateDir, state); err != nil {
		return "", nil, MotherTenantStatus{}, err
	}
	return encodedContext, token, record.MotherTenantStatus, nil
}

func authorizeMotherTenant(stateDir string, encodedContext string) (MotherContext, error) {
	contextValue, err := DecodeMotherContext(encodedContext)
	if err != nil {
		return MotherContext{}, err
	}
	digest := sha256.Sum256([]byte(encodedContext))
	expectedHash := hex.EncodeToString(digest[:])
	motherStateMu.Lock()
	defer motherStateMu.Unlock()
	state, err := loadMotherTenantState(stateDir)
	if err != nil {
		return MotherContext{}, err
	}
	for _, tenant := range state.Tenants {
		if tenant.MotherID != contextValue.MotherID {
			continue
		}
		if tenant.RevokedAt != "" {
			return MotherContext{}, errors.New("Mother Hive access has been revoked")
		}
		if !hmac.Equal([]byte(strings.ToLower(tenant.ContextHash)), []byte(expectedHash)) || tenant.MotherName != contextValue.MotherName {
			return MotherContext{}, errors.New("Mother Hive context does not match its Relay registration")
		}
		return contextValue, nil
	}
	return MotherContext{}, errors.New("Mother Hive is not registered on this Relay")
}

func activeMotherTenantIDs(stateDir string) (map[string]struct{}, error) {
	motherStateMu.Lock()
	defer motherStateMu.Unlock()
	state, err := loadMotherTenantState(stateDir)
	if err != nil {
		return nil, err
	}
	active := make(map[string]struct{}, len(state.Tenants))
	for _, tenant := range state.Tenants {
		if tenant.RevokedAt == "" {
			active[tenant.MotherID] = struct{}{}
		}
	}
	return active, nil
}

func ListMotherTenants(stateDir string) ([]MotherTenantStatus, error) {
	motherStateMu.Lock()
	defer motherStateMu.Unlock()
	state, err := loadMotherTenantState(stateDir)
	if err != nil {
		return nil, err
	}
	out := make([]MotherTenantStatus, 0, len(state.Tenants))
	for _, tenant := range state.Tenants {
		out = append(out, tenant.MotherTenantStatus)
	}
	return out, nil
}

func RevokeMotherTenant(stateDir, motherID string, now time.Time) error {
	motherID = strings.ToLower(strings.TrimSpace(motherID))
	if !motherIDPattern.MatchString(motherID) {
		return errors.New("Mother Hive id is invalid")
	}
	motherStateMu.Lock()
	defer motherStateMu.Unlock()
	state, err := loadMotherTenantState(stateDir)
	if err != nil {
		return err
	}
	for index := range state.Tenants {
		if state.Tenants[index].MotherID != motherID {
			continue
		}
		if state.Tenants[index].RevokedAt == "" {
			state.Tenants[index].RevokedAt = now.UTC().Format(time.RFC3339)
		}
		return saveMotherTenantState(stateDir, state)
	}
	return errors.New("Mother Hive identity was not found")
}

// RevokeAllMotherTenants invalidates every derived local Mother Hive identity
// in one atomic registry update. It is used when the Relay master key rotates.
func RevokeAllMotherTenants(stateDir string, now time.Time) error {
	motherStateMu.Lock()
	defer motherStateMu.Unlock()
	state, err := loadMotherTenantState(stateDir)
	if err != nil {
		return err
	}
	revokedAt := now.UTC().Format(time.RFC3339)
	changed := false
	for index := range state.Tenants {
		if state.Tenants[index].RevokedAt == "" {
			state.Tenants[index].RevokedAt = revokedAt
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return saveMotherTenantState(stateDir, state)
}

func federationMotherID(encodedContext string) string {
	digest := sha256.Sum256([]byte(encodedContext))
	return "federation-" + hex.EncodeToString(digest[:12])
}

func normalizeReceiptMotherID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return LegacyMotherID
	}
	return value
}

func validMotherTenantID(value string) bool {
	value = normalizeReceiptMotherID(value)
	return value == LegacyMotherID || motherIDPattern.MatchString(value) || federatedMotherIDPattern.MatchString(value)
}
