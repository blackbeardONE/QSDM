package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func streamReplayFixture(t *testing.T) (chain.StreamAction, *mempool.Tx) {
	t.Helper()
	payerPublic, payerPrivate, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	providerPublic, _, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payerBytes, _ := payerPublic.MarshalBinary()
	providerBytes, _ := providerPublic.MarshalBinary()
	payerHash := sha256.Sum256(payerBytes)
	providerHash := sha256.Sum256(providerBytes)
	sessionPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	deviceHash := sha256.Sum256([]byte("persisted-stream-device"))
	action := chain.StreamAction{
		ID:                 "persisted-stream-open",
		Sender:             hex.EncodeToString(payerHash[:]),
		StreamID:           "persisted-stream-1",
		Action:             chain.StreamActionOpen,
		Provider:           hex.EncodeToString(providerHash[:]),
		ServiceID:          "qsdm-vpn",
		DeviceIDHash:       hex.EncodeToString(deviceHash[:]),
		SessionPublicKey:   hex.EncodeToString(sessionPublic),
		PriceDust:          2 * chain.DustPerCell,
		PricePeriodSeconds: 2_592_000,
		BudgetDust:         2 * chain.DustPerCell,
		MaxActiveSeconds:   2_592_000,
		ExpiresAt:          "2026-09-01T00:00:00Z",
		Timestamp:          "2026-08-01T00:00:00Z",
	}
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(payerPrivate, payload, nil, true, signature); err != nil {
		t.Fatal(err)
	}
	return action, &mempool.Tx{
		ID:         action.ID,
		Sender:     action.Sender,
		Amount:     0,
		Payload:    payload,
		ContractID: chain.StreamContractID,
		Signature:  hex.EncodeToString(signature),
		PublicKey:  hex.EncodeToString(payerBytes),
	}
}

func TestEvaluatePersistedStateRestoresCELLStreams(t *testing.T) {
	action, tx := streamReplayFixture(t)
	accounts := chain.NewAccountStore()
	accounts.Credit(action.Sender, 10)
	liveStreams := chain.NewStreamStateStore()
	liveAware := chain.NewEnrollmentAwareApplier(accounts, nil)
	liveAware.SetTaskStateStore(chain.NewTaskStateStore())
	liveAware.SetStreamStateStore(liveStreams)
	if err := liveAware.ApplyTx(tx); err != nil {
		t.Fatalf("apply stream before persistence: %v", err)
	}
	wantRoot := liveAware.StateRoot()
	block := &chain.Block{Height: 1, Transactions: []*mempool.Tx{tx}, StateRoot: wantRoot}

	restored, err := evaluatePersistedState(accounts.Clone(), []*chain.Block{block})
	if err != nil {
		t.Fatalf("evaluate persisted stream state: %v", err)
	}
	if restored.streamActions != 1 || restored.stateRoot != wantRoot {
		t.Fatalf("restored stream projection = %+v, want one action/root %s", restored, wantRoot)
	}
	state, ok := restored.streamState.GetStream(action.StreamID)
	if !ok || state.BudgetDust != action.BudgetDust || state.Status != chain.StreamStatusActive {
		t.Fatalf("restored stream state = %+v", state)
	}
}
