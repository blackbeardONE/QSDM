package edgepool

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

const (
	relaySigningKeyFileName = "relay-signing-key.json"
	settlementStateFileName = "settlement-state.json"
	relaySigningKeyVersion  = 1
	settlementStateVersion  = 2
	legacySettlementVersion = 1
)

var (
	errSettlementNotBound        = errors.New("Relay settlement wallets are not bound")
	errNoSettlementReceipts      = errors.New("no unconsumed verified receipts are available for this resource")
	errSettlementBindingConflict = errors.New("Relay settlement wallets are already bound to different addresses")
	errSettlementProofNotFound   = errors.New("pending settlement proof was not found")
)

type relaySigningKeyFile struct {
	Version    int    `json:"version"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

type tenantSettlementState struct {
	Binding            *SettlementBinding               `json:"binding,omitempty"`
	Pending            map[ResourceKind]PoolProof       `json:"pending"`
	AcknowledgedProofs map[string]SettlementAckResponse `json:"acknowledged_proofs"`
}

type settlementState struct {
	Version          int                              `json:"version"`
	Tenants          map[string]tenantSettlementState `json:"tenants"`
	ConsumedReceipts map[string]string                `json:"consumed_receipts"`
}

type legacySettlementState struct {
	Version            int                              `json:"version"`
	Binding            *SettlementBinding               `json:"binding,omitempty"`
	Pending            map[ResourceKind]PoolProof       `json:"pending"`
	ConsumedReceipts   map[string]string                `json:"consumed_receipts"`
	AcknowledgedProofs map[string]SettlementAckResponse `json:"acknowledged_proofs"`
}

type settlementProofIdentity struct {
	ProtocolVersion   string       `json:"protocol_version"`
	SettlementVersion string       `json:"settlement_version"`
	CoordinatorID     string       `json:"coordinator_id"`
	Resource          ResourceKind `json:"resource"`
	WindowStart       string       `json:"window_start"`
	WindowEnd         string       `json:"window_end"`
	WorkerCount       int          `json:"worker_count"`
	JobCount          int          `json:"job_count"`
	TotalUnits        uint64       `json:"total_units"`
	TotalMemoryMiB    uint64       `json:"total_memory_mib"`
	ReceiptRoot       string       `json:"receipt_root"`
	ReceiptIDs        []string     `json:"receipt_ids"`
	ContributorWallet string       `json:"contributor_wallet"`
	MotherHiveWallet  string       `json:"mother_hive_wallet"`
	EcosystemWallet   string       `json:"ecosystem_wallet"`
	RelayPublicKey    string       `json:"relay_public_key"`
}

type settlementProofSignedBody struct {
	Domain  string                  `json:"domain"`
	ProofID string                  `json:"proof_id"`
	Proof   settlementProofIdentity `json:"proof"`
}

func newSettlementState() settlementState {
	return settlementState{
		Version:          settlementStateVersion,
		Tenants:          map[string]tenantSettlementState{},
		ConsumedReceipts: map[string]string{},
	}
}

func newTenantSettlementState() tenantSettlementState {
	return tenantSettlementState{
		Pending:            map[ResourceKind]PoolProof{},
		AcknowledgedProofs: map[string]SettlementAckResponse{},
	}
}

func cloneSettlementState(state settlementState) settlementState {
	out := newSettlementState()
	out.Version = state.Version
	for motherID, tenant := range state.Tenants {
		cloned := newTenantSettlementState()
		if tenant.Binding != nil {
			binding := *tenant.Binding
			cloned.Binding = &binding
		}
		for resource, proof := range tenant.Pending {
			proof.ReceiptIDs = append([]string(nil), proof.ReceiptIDs...)
			cloned.Pending[resource] = proof
		}
		for proofID, acknowledgement := range tenant.AcknowledgedProofs {
			cloned.AcknowledgedProofs[proofID] = acknowledgement
		}
		out.Tenants[motherID] = cloned
	}
	for receiptID, proofID := range state.ConsumedReceipts {
		out.ConsumedReceipts[receiptID] = proofID
	}
	return out
}

func settlementTenant(state settlementState, motherID string) tenantSettlementState {
	motherID = normalizeReceiptMotherID(motherID)
	stored, ok := state.Tenants[motherID]
	if !ok {
		return newTenantSettlementState()
	}
	tenant := newTenantSettlementState()
	if stored.Binding != nil {
		binding := *stored.Binding
		tenant.Binding = &binding
	}
	for resource, proof := range stored.Pending {
		proof.ReceiptIDs = append([]string(nil), proof.ReceiptIDs...)
		tenant.Pending[resource] = proof
	}
	for proofID, acknowledgement := range stored.AcknowledgedProofs {
		tenant.AcknowledgedProofs[proofID] = acknowledgement
	}
	return tenant
}

func validSettlementWallet(address string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(address))
	return err == nil && len(decoded) == sha256.Size
}

// ValidateSettlementRelayPublicKey validates the canonical ML-DSA-87 public
// key representation used by task manifests and settlement proofs.
func ValidateSettlementRelayPublicKey(encoded string) error {
	publicKeyBytes, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(encoded)))
	if err != nil || len(publicKeyBytes) != mldsa87.PublicKeySize {
		return fmt.Errorf("Relay public key must be %d-byte ML-DSA-87 hex", mldsa87.PublicKeySize)
	}
	var publicKey mldsa87.PublicKey
	if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
		return fmt.Errorf("decode Relay public key: %w", err)
	}
	return nil
}

// SettlementRelayID derives the consensus identity of a Relay from its
// signing key. A caller cannot claim a friendly coordinator name with a
// different key and inherit that Relay's on-chain payout binding.
func SettlementRelayID(encodedPublicKey string) (string, error) {
	canonical := strings.ToLower(strings.TrimSpace(encodedPublicKey))
	if err := ValidateSettlementRelayPublicKey(canonical); err != nil {
		return "", err
	}
	publicKeyBytes, _ := hex.DecodeString(canonical)
	digest := sha256.Sum256(append([]byte("QSDM-EDGE-RELAY-ID\x00"), publicKeyBytes...))
	return hex.EncodeToString(digest[:]), nil
}

func validateSettlementBinding(binding SettlementBinding) error {
	if binding.Version != SettlementProtocolVersion {
		return fmt.Errorf("settlement binding version must be %q", SettlementProtocolVersion)
	}
	if !validSettlementWallet(binding.ContributorWallet) {
		return errors.New("contributor wallet must be a 32-byte hexadecimal QSDM address")
	}
	if !validSettlementWallet(binding.MotherHiveWallet) {
		return errors.New("Mother Hive wallet must be a 32-byte hexadecimal QSDM address")
	}
	if !validSettlementWallet(binding.EcosystemWallet) {
		return errors.New("ecosystem wallet must be a 32-byte hexadecimal QSDM address")
	}
	if !strings.EqualFold(binding.EcosystemWallet, ProductionEcosystemWallet) {
		return errors.New("ecosystem wallet does not match the production QSDM ecosystem reserve")
	}
	if _, err := time.Parse(time.RFC3339Nano, binding.BoundAt); err != nil {
		return errors.New("settlement binding time is invalid")
	}
	return nil
}

func settlementIdentity(proof PoolProof) settlementProofIdentity {
	return settlementProofIdentity{
		ProtocolVersion:   proof.Version,
		SettlementVersion: proof.SettlementVersion,
		CoordinatorID:     proof.CoordinatorID,
		Resource:          proof.Resource,
		WindowStart:       proof.WindowStart,
		WindowEnd:         proof.WindowEnd,
		WorkerCount:       proof.WorkerCount,
		JobCount:          proof.JobCount,
		TotalUnits:        proof.TotalUnits,
		TotalMemoryMiB:    proof.TotalMemoryMiB,
		ReceiptRoot:       strings.ToLower(proof.ReceiptRoot),
		ReceiptIDs:        append([]string(nil), proof.ReceiptIDs...),
		ContributorWallet: strings.ToLower(proof.ContributorWallet),
		MotherHiveWallet:  strings.ToLower(proof.MotherHiveWallet),
		EcosystemWallet:   strings.ToLower(proof.EcosystemWallet),
		RelayPublicKey:    strings.ToLower(proof.RelayPublicKey),
	}
}

// SettlementProofID returns the deterministic identity of a signed payout
// batch. Signatures are intentionally excluded; re-signing the same batch
// cannot create a second payable proof.
func SettlementProofID(proof PoolProof) (string, error) {
	raw, err := json.Marshal(settlementIdentity(proof))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("QSDM-EDGE-SETTLEMENT-ID\x00"), raw...))
	return hex.EncodeToString(digest[:]), nil
}

// SettlementProofCanonicalBytes returns the exact ML-DSA-87 signed message.
func SettlementProofCanonicalBytes(proof PoolProof) ([]byte, error) {
	body := settlementProofSignedBody{
		Domain:  SettlementProtocolVersion,
		ProofID: strings.ToLower(proof.ProofID),
		Proof:   settlementIdentity(proof),
	}
	return json.Marshal(body)
}

// VerifySettlementPoolProof verifies the Relay's public-key signature and
// deterministic proof identity. Consensus performs additional task, wallet,
// time-window, and global replay checks.
func VerifySettlementPoolProof(proof PoolProof) error {
	if proof.Version != ProtocolVersion {
		return fmt.Errorf("pool proof version must be %q", ProtocolVersion)
	}
	if proof.SettlementVersion != SettlementProtocolVersion {
		return fmt.Errorf("settlement proof version must be %q", SettlementProtocolVersion)
	}
	if err := ValidateWorkerID(proof.CoordinatorID); err != nil {
		return fmt.Errorf("invalid settlement coordinator: %w", err)
	}
	expectedRelayID, err := SettlementRelayID(proof.RelayPublicKey)
	if err != nil {
		return err
	}
	if !strings.EqualFold(proof.CoordinatorID, expectedRelayID) {
		return errors.New("settlement coordinator id is not derived from the Relay public key")
	}
	if !proof.Resource.Valid() {
		return errors.New("settlement resource must be cpu, gpu, or ram")
	}
	if !validSettlementWallet(proof.ContributorWallet) ||
		!validSettlementWallet(proof.MotherHiveWallet) ||
		!validSettlementWallet(proof.EcosystemWallet) {
		return errors.New("settlement proof contains an invalid payout wallet")
	}
	if proof.JobCount <= 0 || proof.WorkerCount <= 0 || proof.WorkerCount > proof.JobCount {
		return errors.New("settlement proof has invalid worker or job accounting")
	}
	if proof.JobCount != len(proof.ReceiptIDs) || proof.TotalUnits == 0 {
		return errors.New("settlement proof receipt count or units do not match")
	}
	if len(proof.ReceiptIDs) > 512 {
		return errors.New("settlement proof exceeds the 512-receipt consensus limit")
	}
	if decoded, err := hex.DecodeString(proof.ReceiptRoot); err != nil || len(decoded) != sha256.Size {
		return errors.New("settlement receipt root is invalid")
	}
	seen := make(map[string]struct{}, len(proof.ReceiptIDs))
	previous := ""
	for _, receiptID := range proof.ReceiptIDs {
		canonical := strings.ToLower(strings.TrimSpace(receiptID))
		decoded, err := hex.DecodeString(canonical)
		if err != nil || len(decoded) != sha256.Size {
			return errors.New("settlement proof contains an invalid receipt id")
		}
		if _, duplicate := seen[canonical]; duplicate {
			return errors.New("settlement proof contains a duplicate receipt id")
		}
		if previous != "" && canonical <= previous {
			return errors.New("settlement receipt ids must be strictly sorted")
		}
		seen[canonical] = struct{}{}
		previous = canonical
	}
	expectedID, err := SettlementProofID(proof)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expectedID, proof.ProofID) {
		return errors.New("settlement proof id does not match its canonical contents")
	}
	publicKeyBytes, _ := hex.DecodeString(proof.RelayPublicKey)
	signatureBytes, err := hex.DecodeString(proof.RelaySignature)
	if err != nil || len(signatureBytes) != mldsa87.SignatureSize {
		return fmt.Errorf("Relay signature must be %d-byte ML-DSA-87 hex", mldsa87.SignatureSize)
	}
	canonical, err := SettlementProofCanonicalBytes(proof)
	if err != nil {
		return err
	}
	var publicKey mldsa87.PublicKey
	if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
		return fmt.Errorf("decode Relay public key: %w", err)
	}
	if !mldsa87.Verify(&publicKey, canonical, nil, signatureBytes) {
		return errors.New("Relay ML-DSA-87 settlement signature did not verify")
	}
	return nil
}

func loadOrCreateRelaySigningKey(stateDir string) (*mldsa87.PrivateKey, string, error) {
	path := filepath.Join(stateDir, relaySigningKeyFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		publicKey, privateKey, generateErr := mldsa87.GenerateKey(rand.Reader)
		if generateErr != nil {
			return nil, "", fmt.Errorf("generate Relay ML-DSA-87 key: %w", generateErr)
		}
		publicBytes, marshalErr := publicKey.MarshalBinary()
		if marshalErr != nil {
			return nil, "", marshalErr
		}
		privateBytes, marshalErr := privateKey.MarshalBinary()
		if marshalErr != nil {
			return nil, "", marshalErr
		}
		encoded, marshalErr := json.MarshalIndent(relaySigningKeyFile{
			Version:    relaySigningKeyVersion,
			PublicKey:  hex.EncodeToString(publicBytes),
			PrivateKey: hex.EncodeToString(privateBytes),
		}, "", "  ")
		if marshalErr != nil {
			return nil, "", marshalErr
		}
		encoded = append(encoded, '\n')
		if writeErr := fileutil.WriteFileAtomic(path, encoded, 0o600); writeErr != nil {
			return nil, "", fmt.Errorf("persist Relay ML-DSA-87 key: %w", writeErr)
		}
		return privateKey, hex.EncodeToString(publicBytes), nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read Relay ML-DSA-87 key: %w", err)
	}
	var encoded relaySigningKeyFile
	if err := json.Unmarshal(raw, &encoded); err != nil || encoded.Version != relaySigningKeyVersion {
		return nil, "", errors.New("Relay signing key file is invalid")
	}
	publicBytes, err := hex.DecodeString(encoded.PublicKey)
	if err != nil || len(publicBytes) != mldsa87.PublicKeySize {
		return nil, "", errors.New("Relay signing public key is invalid")
	}
	privateBytes, err := hex.DecodeString(encoded.PrivateKey)
	if err != nil {
		return nil, "", errors.New("Relay signing private key is invalid")
	}
	var publicKey mldsa87.PublicKey
	var privateKey mldsa87.PrivateKey
	if err := publicKey.UnmarshalBinary(publicBytes); err != nil {
		return nil, "", fmt.Errorf("decode Relay signing public key: %w", err)
	}
	if err := privateKey.UnmarshalBinary(privateBytes); err != nil {
		return nil, "", fmt.Errorf("decode Relay signing private key: %w", err)
	}
	challenge := []byte("QSDM-EDGE-RELAY-KEY-CHECK")
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(&privateKey, challenge, nil, true, signature); err != nil ||
		!mldsa87.Verify(&publicKey, challenge, nil, signature) {
		return nil, "", errors.New("Relay signing key pair does not match")
	}
	_ = os.Chmod(path, 0o600)
	return &privateKey, strings.ToLower(encoded.PublicKey), nil
}

func loadSettlementState(stateDir string, relayPublicKey string) (settlementState, error) {
	path := filepath.Join(stateDir, settlementStateFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newSettlementState(), nil
	}
	if err != nil {
		return settlementState{}, fmt.Errorf("read Relay settlement state: %w", err)
	}
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return settlementState{}, errors.New("Relay settlement state is invalid")
	}
	state := newSettlementState()
	switch header.Version {
	case legacySettlementVersion:
		var legacy legacySettlementState
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return settlementState{}, errors.New("Relay settlement state is invalid")
		}
		state.ConsumedReceipts = legacy.ConsumedReceipts
		state.Tenants[LegacyMotherID] = tenantSettlementState{
			Binding:            legacy.Binding,
			Pending:            legacy.Pending,
			AcknowledgedProofs: legacy.AcknowledgedProofs,
		}
	case settlementStateVersion:
		if err := json.Unmarshal(raw, &state); err != nil {
			return settlementState{}, errors.New("Relay settlement state is invalid")
		}
	default:
		return settlementState{}, errors.New("Relay settlement state has an unsupported version")
	}
	if state.Tenants == nil {
		state.Tenants = map[string]tenantSettlementState{}
	}
	if state.ConsumedReceipts == nil {
		state.ConsumedReceipts = map[string]string{}
	}
	for motherID := range state.Tenants {
		if !validMotherTenantID(motherID) {
			return settlementState{}, errors.New("persisted settlement has an invalid Mother Hive identity")
		}
		tenant := settlementTenant(state, motherID)
		if tenant.Binding != nil {
			if err := validateSettlementBinding(*tenant.Binding); err != nil {
				return settlementState{}, fmt.Errorf("invalid persisted settlement binding: %w", err)
			}
		}
		for resource, proof := range tenant.Pending {
			if resource != proof.Resource || !strings.EqualFold(proof.RelayPublicKey, relayPublicKey) {
				return settlementState{}, errors.New("persisted settlement proof does not match this Relay key")
			}
			if err := VerifySettlementPoolProof(proof); err != nil {
				return settlementState{}, fmt.Errorf("invalid persisted settlement proof: %w", err)
			}
			if tenant.Binding == nil ||
				!strings.EqualFold(proof.ContributorWallet, tenant.Binding.ContributorWallet) ||
				!strings.EqualFold(proof.MotherHiveWallet, tenant.Binding.MotherHiveWallet) ||
				!strings.EqualFold(proof.EcosystemWallet, tenant.Binding.EcosystemWallet) {
				return settlementState{}, errors.New("persisted settlement proof does not match the Mother Hive payout binding")
			}
		}
		state.Tenants[motherID] = tenant
	}
	return state, nil
}

func saveSettlementState(path string, state settlementState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return fileutil.WriteFileAtomic(path, raw, 0o600)
}

func settlementPendingProofIDs(state tenantSettlementState) map[ResourceKind]string {
	out := make(map[ResourceKind]string, len(state.Pending))
	for resource, proof := range state.Pending {
		out[resource] = proof.ProofID
	}
	return out
}

func sortedUnconsumedReceipts(receipts []Receipt, consumed map[string]string, motherID string, resource ResourceKind, cutoff time.Time, limit int) []Receipt {
	selected := make([]Receipt, 0, limit)
	for _, receipt := range receipts {
		if receipt.Resource != resource || normalizeReceiptMotherID(receipt.MotherID) != normalizeReceiptMotherID(motherID) {
			continue
		}
		if _, used := consumed[strings.ToLower(receipt.ReceiptID)]; used {
			continue
		}
		accepted, err := time.Parse(time.RFC3339Nano, receipt.AcceptedAt)
		if err != nil || accepted.Before(cutoff) {
			continue
		}
		selected = append(selected, receipt)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ReceiptID < selected[j].ReceiptID })
	if len(selected) > limit {
		selected = selected[:limit]
	}
	return selected
}

func (c *Coordinator) settlementPath() string {
	return filepath.Join(c.config.StateDir, settlementStateFileName)
}

// BindSettlement fixes payout destinations for this Relay. The operation is
// idempotent for the same addresses and rejects remote rebinding.
func (c *Coordinator) BindSettlement(request SettlementBindRequest, now time.Time) (SettlementBinding, error) {
	return c.BindSettlementForMother(LegacyMotherID, request, now)
}

func (c *Coordinator) BindSettlementForMother(motherID string, request SettlementBindRequest, now time.Time) (SettlementBinding, error) {
	motherID = normalizeReceiptMotherID(motherID)
	if !validMotherTenantID(motherID) {
		return SettlementBinding{}, errors.New("Mother Hive identity is invalid")
	}
	binding := SettlementBinding{
		Version:           strings.TrimSpace(request.Version),
		ContributorWallet: strings.ToLower(strings.TrimSpace(request.ContributorWallet)),
		MotherHiveWallet:  strings.ToLower(strings.TrimSpace(request.MotherHiveWallet)),
		EcosystemWallet:   strings.ToLower(strings.TrimSpace(request.EcosystemWallet)),
		BoundAt:           now.UTC().Format(time.RFC3339Nano),
	}
	if err := validateSettlementBinding(binding); err != nil {
		return SettlementBinding{}, err
	}

	c.settlePersistMu.Lock()
	defer c.settlePersistMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	tenant := settlementTenant(c.settlement, motherID)
	if tenant.Binding != nil {
		existing := *tenant.Binding
		if existing.Version == binding.Version &&
			strings.EqualFold(existing.ContributorWallet, binding.ContributorWallet) &&
			strings.EqualFold(existing.MotherHiveWallet, binding.MotherHiveWallet) &&
			strings.EqualFold(existing.EcosystemWallet, binding.EcosystemWallet) {
			return existing, nil
		}
		return SettlementBinding{}, errSettlementBindingConflict
	}
	next := cloneSettlementState(c.settlement)
	tenant.Binding = &binding
	next.Tenants[motherID] = tenant
	if err := saveSettlementState(c.settlementPath(), next); err != nil {
		return SettlementBinding{}, fmt.Errorf("persist Relay settlement binding: %w", err)
	}
	c.settlement = next
	return binding, nil
}

// LatestSettlementProof returns the same pending proof until the Mother Hive
// acknowledges that exact proof after a successful chain commit.
func (c *Coordinator) LatestSettlementProof(resource ResourceKind, now time.Time) (PoolProof, error) {
	return c.LatestSettlementProofForMother(LegacyMotherID, resource, now)
}

func (c *Coordinator) LatestSettlementProofForMother(motherID string, resource ResourceKind, now time.Time) (PoolProof, error) {
	motherID = normalizeReceiptMotherID(motherID)
	if !validMotherTenantID(motherID) {
		return PoolProof{}, errors.New("Mother Hive identity is invalid")
	}
	if !resource.Valid() {
		return PoolProof{}, errors.New("settlement resource must be cpu, gpu, or ram")
	}
	c.settlePersistMu.Lock()
	defer c.settlePersistMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()

	tenant := settlementTenant(c.settlement, motherID)
	if pending, ok := tenant.Pending[resource]; ok {
		pending.ReceiptIDs = append([]string(nil), pending.ReceiptIDs...)
		return pending, nil
	}
	if tenant.Binding == nil {
		return PoolProof{}, errSettlementNotBound
	}
	selected := sortedUnconsumedReceipts(
		c.receipts,
		c.settlement.ConsumedReceipts,
		motherID,
		resource,
		now.UTC().Add(-c.config.ProofWindow),
		c.config.MaxProofReceipts,
	)
	if len(selected) == 0 {
		return PoolProof{}, errNoSettlementReceipts
	}
	relayID, err := SettlementRelayID(c.settlementPublicKey)
	if err != nil {
		return PoolProof{}, err
	}
	proof := AggregateReceipts(relayID, resource, selected, now.UTC())
	proof.SettlementVersion = SettlementProtocolVersion
	proof.ContributorWallet = tenant.Binding.ContributorWallet
	proof.MotherHiveWallet = tenant.Binding.MotherHiveWallet
	proof.EcosystemWallet = tenant.Binding.EcosystemWallet
	proof.RelayPublicKey = c.settlementPublicKey
	proofID, err := SettlementProofID(proof)
	if err != nil {
		return PoolProof{}, err
	}
	proof.ProofID = proofID
	proof.Signature = PoolProofSignature(c.config.MotherToken, proof)
	canonical, err := SettlementProofCanonicalBytes(proof)
	if err != nil {
		return PoolProof{}, err
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(c.settlementSigner, canonical, nil, true, signature); err != nil {
		return PoolProof{}, fmt.Errorf("sign Relay settlement proof: %w", err)
	}
	proof.RelaySignature = hex.EncodeToString(signature)
	if err := VerifySettlementPoolProof(proof); err != nil {
		return PoolProof{}, fmt.Errorf("self-verify Relay settlement proof: %w", err)
	}

	next := cloneSettlementState(c.settlement)
	tenant.Pending[resource] = proof
	next.Tenants[motherID] = tenant
	if err := saveSettlementState(c.settlementPath(), next); err != nil {
		return PoolProof{}, fmt.Errorf("persist pending Relay settlement proof: %w", err)
	}
	c.settlement = next
	return proof, nil
}

// AcknowledgeSettlementProof consumes a batch only after Hive observed its
// task action committed by QSDM Core. Repeated acknowledgement is idempotent.
func (c *Coordinator) AcknowledgeSettlementProof(request SettlementAckRequest, now time.Time) (SettlementAckResponse, error) {
	return c.AcknowledgeSettlementProofForMother(LegacyMotherID, request, now)
}

func (c *Coordinator) AcknowledgeSettlementProofForMother(motherID string, request SettlementAckRequest, now time.Time) (SettlementAckResponse, error) {
	motherID = normalizeReceiptMotherID(motherID)
	if !validMotherTenantID(motherID) {
		return SettlementAckResponse{}, errors.New("Mother Hive identity is invalid")
	}
	if strings.TrimSpace(request.Version) != SettlementProtocolVersion {
		return SettlementAckResponse{}, fmt.Errorf("settlement acknowledgement version must be %q", SettlementProtocolVersion)
	}
	proofID := strings.ToLower(strings.TrimSpace(request.ProofID))
	if decoded, err := hex.DecodeString(proofID); err != nil || len(decoded) != sha256.Size {
		return SettlementAckResponse{}, errors.New("settlement acknowledgement proof id is invalid")
	}

	c.settlePersistMu.Lock()
	defer c.settlePersistMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	tenant := settlementTenant(c.settlement, motherID)
	if acknowledged, ok := tenant.AcknowledgedProofs[proofID]; ok {
		return acknowledged, nil
	}
	var resource ResourceKind
	var proof PoolProof
	for candidateResource, candidate := range tenant.Pending {
		if strings.EqualFold(candidate.ProofID, proofID) {
			resource = candidateResource
			proof = candidate
			break
		}
	}
	if !resource.Valid() {
		return SettlementAckResponse{}, errSettlementProofNotFound
	}
	acknowledgement := SettlementAckResponse{
		OK:               true,
		ProofID:          proofID,
		Resource:         resource,
		ConsumedReceipts: len(proof.ReceiptIDs),
		AcknowledgedAt:   now.UTC().Format(time.RFC3339Nano),
	}
	next := cloneSettlementState(c.settlement)
	delete(tenant.Pending, resource)
	for _, receiptID := range proof.ReceiptIDs {
		next.ConsumedReceipts[strings.ToLower(receiptID)] = proofID
	}
	tenant.AcknowledgedProofs[proofID] = acknowledgement
	next.Tenants[motherID] = tenant
	if err := saveSettlementState(c.settlementPath(), next); err != nil {
		return SettlementAckResponse{}, fmt.Errorf("persist Relay settlement acknowledgement: %w", err)
	}
	c.settlement = next
	return acknowledgement, nil
}
