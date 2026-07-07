package chain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func signedPooledProof(t *testing.T, contributor, mother string, receiptIDs []string, now time.Time) (edgepool.PoolProof, *mldsa87.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := publicKey.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	encodedPublicKey := hex.EncodeToString(publicBytes)
	coordinatorID, err := edgepool.SettlementRelayID(encodedPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	proof := edgepool.PoolProof{
		Version:           edgepool.ProtocolVersion,
		CoordinatorID:     coordinatorID,
		Resource:          edgepool.ResourceCPU,
		WindowStart:       now.Add(-time.Minute).Format(time.RFC3339Nano),
		WindowEnd:         now.Format(time.RFC3339Nano),
		WorkerCount:       1,
		JobCount:          len(receiptIDs),
		TotalUnits:        uint64(len(receiptIDs)) * 100,
		ReceiptRoot:       strings.Repeat("f", 64),
		ReceiptIDs:        append([]string(nil), receiptIDs...),
		SettlementVersion: edgepool.SettlementProtocolVersion,
		ContributorWallet: contributor,
		MotherHiveWallet:  mother,
		EcosystemWallet:   edgepool.ProductionEcosystemWallet,
		RelayPublicKey:    encodedPublicKey,
	}
	proof.ProofID, err = edgepool.SettlementProofID(proof)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := edgepool.SettlementProofCanonicalBytes(proof)
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(privateKey, canonical, nil, true, signature); err != nil {
		t.Fatal(err)
	}
	proof.RelaySignature = hex.EncodeToString(signature)
	return proof, privateKey
}

func registerPooledTask(t *testing.T, store *TaskStateStore, taskID, relayID string, now time.Time) {
	t.Helper()
	manifest := TaskManifest{
		SchemaVersion:      TaskCatalogSchemaVersion,
		TaskID:             taskID,
		Version:            1,
		Name:               "Authorized pooled resource task",
		Manager:            "manager",
		Active:             true,
		MinimumStakeAmount: 1,
		RewardPerRound:     0.05,
		RoundTime:          60,
		AuthorizedRelayIDs: []string{relayID},
		Runtime: TaskRuntimeManifest{
			Kind:       "capability",
			Capability: "qsdm-edge-worker-v1",
		},
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyAction(TaskAction{
		ID: "catalog-register-" + taskID, Sender: manifest.Manager, TaskID: taskID,
		Action: TaskActionCatalogRegister, Payload: string(payload),
		Timestamp: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
}

func resignPooledProof(t *testing.T, proof edgepool.PoolProof, privateKey *mldsa87.PrivateKey) edgepool.PoolProof {
	t.Helper()
	var err error
	proof.ProofID, err = edgepool.SettlementProofID(proof)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := edgepool.SettlementProofCanonicalBytes(proof)
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(privateKey, canonical, nil, true, signature); err != nil {
		t.Fatal(err)
	}
	proof.RelaySignature = hex.EncodeToString(signature)
	return proof
}

func pooledSubmitTx(t *testing.T, id string, nonce uint64, round, slot uint64, proof edgepool.PoolProof, reward float64, timestamp time.Time) *mempool.Tx {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"round": round, "slot": slot,
		"source":   edgepool.SettlementProofSource,
		"resource": "cpu", "submission_value": proof.ReceiptRoot,
		"reward_amount": reward, "proof": proof,
	})
	if err != nil {
		t.Fatal(err)
	}
	action := TaskAction{
		ID: id, Sender: proof.MotherHiveWallet, TaskID: "qsdm-edge-worker",
		Action: "submit", Payload: string(payload), Nonce: nonce,
		Timestamp: timestamp.Format(time.RFC3339Nano),
	}
	actionJSON, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{
		ID: id, Sender: proof.MotherHiveWallet, Nonce: nonce,
		ContractID: TaskContractID, Payload: actionJSON,
	}
}

func TestPooledSettlementPaysAtomicallyAndRejectsReplay(t *testing.T) {
	const (
		contributor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		mother      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	now := time.Now().UTC()
	proof, relayKey := signedPooledProof(t, contributor, mother,
		[]string{strings.Repeat("1", 64), strings.Repeat("2", 64)}, now)
	store := NewTaskStateStore()
	registerPooledTask(t, store, "qsdm-edge-worker", proof.CoordinatorID, now)
	if err := store.ApplyActions([]TaskAction{
		{ID: "stake", Sender: mother, TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)},
		{ID: "fund", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)},
	}); err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(mother, 1)
	tx := pooledSubmitTx(t, "settle-1", 0, 2, 120, proof, 0.05, now.Add(time.Second))
	if err := store.ApplyEconomicTxAtHeight(tx, accounts, 121); err != nil {
		t.Fatal(err)
	}
	assertBalance := func(address string, want float64) {
		t.Helper()
		account, ok := accounts.Get(address)
		if !ok || mathAbs(account.Balance-want) > taskAmountEpsilon {
			t.Fatalf("balance %s = %+v, want %.8f", address, account, want)
		}
	}
	assertBalance(contributor, 0.035)
	assertBalance(mother, 1.0075)
	assertBalance(edgepool.ProductionEcosystemWallet, 0.0075)
	state, ok := store.GetTask("qsdm-edge-worker")
	if !ok || mathAbs(state.RewardPoolAmount-0.95) > taskAmountEpsilon || state.PendingRewardAmount != 0 || mathAbs(state.TotalRewardPaidAmount-0.05) > taskAmountEpsilon {
		t.Fatalf("unexpected settled task accounting: %+v", state)
	}
	submission := state.Submissions["2"][mother]
	if !submission.Claimed || submission.SettlementProofID != proof.ProofID ||
		mathAbs(submission.SettlementContributorAmount-0.035) > taskAmountEpsilon ||
		mathAbs(submission.SettlementMotherHiveAmount-0.0075) > taskAmountEpsilon ||
		mathAbs(submission.SettlementEcosystemAmount-0.0075) > taskAmountEpsilon {
		t.Fatalf("settlement details were not committed: %+v", submission)
	}

	replay := pooledSubmitTx(t, "settle-replay", 1, 3, 180, proof, 0.05, now.Add(2*time.Second))
	if err := store.ApplyEconomicTxAtHeight(replay, accounts, 181); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("proof replay returned %v", err)
	}
	assertBalance(contributor, 0.035)
	assertBalance(mother, 1.0075)
	assertBalance(edgepool.ProductionEcosystemWallet, 0.0075)

	reusedReceiptProof := proof
	reusedReceiptProof.WindowStart = now.Add(-30 * time.Second).Format(time.RFC3339Nano)
	reusedReceiptProof = resignPooledProof(t, reusedReceiptProof, relayKey)
	reusedReceipt := pooledSubmitTx(t, "settle-reused-receipt", 1, 3, 180, reusedReceiptProof, 0.05, now.Add(3*time.Second))
	if err := store.ApplyEconomicTxAtHeight(reusedReceipt, accounts, 181); err == nil || !strings.Contains(err.Error(), "receipt was already consumed") {
		t.Fatalf("receipt replay returned %v", err)
	}
	assertBalance(contributor, 0.035)
	assertBalance(mother, 1.0075)
	assertBalance(edgepool.ProductionEcosystemWallet, 0.0075)
}

func TestPooledSettlementRequiresEconomicHeightAndRejectsTampering(t *testing.T) {
	const (
		contributor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		mother      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	now := time.Now().UTC()
	proof, _ := signedPooledProof(t, contributor, mother,
		[]string{strings.Repeat("3", 64)}, now)
	tx := pooledSubmitTx(t, "settle-gate", 0, 2, 120, proof, 0.05, now.Add(time.Second))
	action, err := DecodeTaskActionTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	store := NewTaskStateStore()
	if err := store.ApplyAction(action); !errors.Is(err, ErrPooledSettlementRequiresEconomicApply) {
		t.Fatalf("direct settlement action returned %v", err)
	}
	accounts := NewAccountStore()
	accounts.Credit(mother, 1)
	if err := store.ApplyEconomicTx(tx, accounts); err == nil || !strings.Contains(err.Error(), "consensus block height") {
		t.Fatalf("heightless settlement returned %v", err)
	}
	proof.TotalUnits++
	tampered := pooledSubmitTx(t, "settle-tampered", 0, 2, 120, proof, 0.05, now.Add(time.Second))
	if err := store.ApplyEconomicTxAtHeight(tampered, accounts, 121); err == nil || !strings.Contains(err.Error(), "verify Relay settlement proof") {
		t.Fatalf("tampered settlement returned %v", err)
	}
}

func TestPooledSettlementRequiresManifestAuthorizedRelay(t *testing.T) {
	const (
		contributor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		mother      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	now := time.Now().UTC()
	authorized, _ := signedPooledProof(t, contributor, mother,
		[]string{strings.Repeat("4", 64)}, now)
	unauthorized, _ := signedPooledProof(t, contributor, mother,
		[]string{strings.Repeat("5", 64)}, now)
	store := NewTaskStateStore()
	registerPooledTask(t, store, "qsdm-edge-worker", authorized.CoordinatorID, now)
	if err := store.ApplyActions([]TaskAction{
		{ID: "stake-auth", Sender: mother, TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)},
		{ID: "fund-auth", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)},
	}); err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(mother, 1)
	tx := pooledSubmitTx(t, "settle-unauthorized", 0, 2, 120, unauthorized, 0.05, now.Add(time.Second))
	if err := store.ApplyEconomicTxAtHeight(tx, accounts, 121); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unauthorized Relay settlement returned %v", err)
	}
	if account, ok := accounts.Get(contributor); ok && account.Balance != 0 {
		t.Fatalf("unauthorized Relay credited contributor: %+v", account)
	}
	if account, ok := accounts.Get(mother); !ok || account.Balance != 1 {
		t.Fatalf("unauthorized Relay changed Mother Hive balance: %+v", account)
	}
}

func mathAbs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
