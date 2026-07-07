package edgepool

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func appendTestSettlementReceipt(t *testing.T, relay *Relay, jobID string, acceptedAt time.Time) Receipt {
	t.Helper()
	result := JobResult{
		Version:    ProtocolVersion,
		JobID:      jobID,
		WorkerID:   "agent-a",
		Resource:   ResourceCPU,
		Algorithm:  AlgorithmCPU,
		Digest:     strings.Repeat("a", 64),
		Units:      100,
		DurationMS: 1,
		Completed:  acceptedAt.UTC().Format(time.RFC3339Nano),
	}
	receipt := relay.makeReceipt(result)
	receipt.AcceptedAt = acceptedAt.UTC().Format(time.RFC3339Nano)
	if err := relay.appendReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	relay.mu.Lock()
	relay.receipts = append(relay.receipts, receipt)
	relay.receiptsByJob[receipt.JobID] = receipt
	relay.mu.Unlock()
	return receipt
}

func TestRelaySettlementBindingProofAckSurvivesRestart(t *testing.T) {
	stateDir := t.TempDir()
	relay, err := NewRelay(RelayConfig{
		ID: "relay-settlement", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: stateDir, ProofWindow: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	request := SettlementBindRequest{
		Version:           SettlementProtocolVersion,
		ContributorWallet: strings.Repeat("a", 64),
		MotherHiveWallet:  strings.Repeat("b", 64),
		EcosystemWallet:   ProductionEcosystemWallet,
	}
	binding, err := relay.BindSettlement(request, now)
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := relay.BindSettlement(request, now.Add(time.Minute)); err != nil || repeated.BoundAt != binding.BoundAt {
		t.Fatalf("idempotent bind = %+v, %v", repeated, err)
	}
	conflict := request
	conflict.ContributorWallet = strings.Repeat("c", 64)
	if _, err := relay.BindSettlement(conflict, now); !errors.Is(err, errSettlementBindingConflict) {
		t.Fatalf("conflicting bind returned %v", err)
	}

	receipt := appendTestSettlementReceipt(t, relay, "job-a", now)
	proof, err := relay.LatestSettlementProof(ResourceCPU, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySettlementPoolProof(proof); err != nil {
		t.Fatalf("signed proof did not verify: %v", err)
	}
	if proof.ContributorWallet != request.ContributorWallet ||
		proof.MotherHiveWallet != request.MotherHiveWallet ||
		proof.EcosystemWallet != ProductionEcosystemWallet ||
		len(proof.ReceiptIDs) != 1 || proof.ReceiptIDs[0] != receipt.ReceiptID {
		t.Fatalf("unexpected settlement proof: %+v", proof)
	}
	if proof.CoordinatorID != relay.settlementRelayID {
		t.Fatalf("proof coordinator %q does not match key-derived Relay id %q", proof.CoordinatorID, relay.settlementRelayID)
	}
	repeatedProof, err := relay.LatestSettlementProof(ResourceCPU, now.Add(2*time.Second))
	if err != nil || repeatedProof.ProofID != proof.ProofID || repeatedProof.RelaySignature != proof.RelaySignature {
		t.Fatalf("pending proof changed before acknowledgement: %+v, %v", repeatedProof, err)
	}

	ack, err := relay.AcknowledgeSettlementProof(SettlementAckRequest{
		Version: SettlementProtocolVersion,
		ProofID: proof.ProofID,
	}, now.Add(3*time.Second))
	if err != nil || ack.ConsumedReceipts != 1 {
		t.Fatalf("acknowledgement = %+v, %v", ack, err)
	}
	if repeatedAck, err := relay.AcknowledgeSettlementProof(SettlementAckRequest{
		Version: SettlementProtocolVersion,
		ProofID: proof.ProofID,
	}, now.Add(4*time.Second)); err != nil || repeatedAck != ack {
		t.Fatalf("idempotent acknowledgement = %+v, %v", repeatedAck, err)
	}

	restarted, err := NewRelay(RelayConfig{
		ID: "relay-settlement", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: stateDir, ProofWindow: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	status := restarted.Status()
	if !status.SettlementReady || status.SettlementRelayID != proof.CoordinatorID || status.SettlementPublicKey != proof.RelayPublicKey || status.SettlementBinding == nil {
		t.Fatalf("settlement identity was not restored: %+v", status)
	}
	if _, err := restarted.LatestSettlementProof(ResourceCPU, now.Add(5*time.Second)); !errors.Is(err, errNoSettlementReceipts) {
		t.Fatalf("consumed receipt was payable after restart: %v", err)
	}
}

func TestVerifySettlementPoolProofRejectsTampering(t *testing.T) {
	relay, err := NewRelay(RelayConfig{
		ID: "relay-tamper", Token: testToken(), StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = relay.BindSettlement(SettlementBindRequest{
		Version:           SettlementProtocolVersion,
		ContributorWallet: strings.Repeat("a", 64),
		MotherHiveWallet:  strings.Repeat("b", 64),
		EcosystemWallet:   ProductionEcosystemWallet,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	appendTestSettlementReceipt(t, relay, "job-tamper", now)
	proof, err := relay.LatestSettlementProof(ResourceCPU, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	proof.TotalUnits++
	if err := VerifySettlementPoolProof(proof); err == nil {
		t.Fatal("tampered settlement proof verified")
	}
	proof = relay.settlement.Pending[ResourceCPU]
	proof.CoordinatorID = "relay-friendly-name"
	if err := VerifySettlementPoolProof(proof); err == nil || !strings.Contains(err.Error(), "not derived") {
		t.Fatalf("non-derived Relay coordinator id returned %v", err)
	}
}
