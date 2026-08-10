package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

type streamTestWallet struct {
	public  *mldsa87.PublicKey
	private *mldsa87.PrivateKey
	address string
}

func newStreamTestWallet(t *testing.T) streamTestWallet {
	t.Helper()
	public, private, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := public.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(publicBytes)
	return streamTestWallet{
		public:  public,
		private: private,
		address: hex.EncodeToString(sum[:]),
	}
}

func signedStreamTx(t *testing.T, wallet streamTestWallet, action StreamAction) *mempool.Tx {
	t.Helper()
	action.Sender = wallet.address
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(wallet.private, payload, nil, true, signature); err != nil {
		t.Fatal(err)
	}
	publicBytes, err := wallet.public.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{
		ID:         action.ID,
		Sender:     action.Sender,
		Amount:     0,
		Nonce:      action.Nonce,
		Payload:    payload,
		ContractID: StreamContractID,
		Signature:  hex.EncodeToString(signature),
		PublicKey:  hex.EncodeToString(publicBytes),
	}
}

func signedUsageReceipt(t *testing.T, private ed25519.PrivateKey, receipt StreamUsageReceipt) StreamUsageReceipt {
	t.Helper()
	message, err := StreamUsageReceiptSigningBytes(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Signature = hex.EncodeToString(ed25519.Sign(private, message))
	return receipt
}

func openStreamAction(t *testing.T, payer, provider streamTestWallet, sessionPublic ed25519.PublicKey, budget uint64) StreamAction {
	t.Helper()
	device := sha256.Sum256([]byte("test-device"))
	return StreamAction{
		ID:                 "stream-open",
		Sender:             payer.address,
		StreamID:           "vpn-device-1",
		Action:             StreamActionOpen,
		Provider:           provider.address,
		ServiceID:          "qsdm-vpn",
		DeviceIDHash:       hex.EncodeToString(device[:]),
		SessionPublicKey:   hex.EncodeToString(sessionPublic),
		PriceDust:          2 * DustPerCell,
		PricePeriodSeconds: 30 * 24 * 60 * 60,
		BudgetDust:         budget,
		MaxActiveSeconds:   30 * 24 * 60 * 60,
		ExpiresAt:          "2026-09-01T00:00:00Z",
		Nonce:              0,
		Timestamp:          "2026-08-01T00:00:00Z",
	}
}

func TestStreamLifecycleEscrowsMetersSettlesAndRefunds(t *testing.T) {
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, sessionPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(payer.address, 10)
	streams := NewStreamStateStore()

	open := openStreamAction(t, payer, provider, sessionPublic, 2*DustPerCell)
	if err := streams.ApplyEconomicTx(signedStreamTx(t, payer, open), accounts); err != nil {
		t.Fatalf("open: %v", err)
	}
	payerAccount, _ := accounts.Get(payer.address)
	if got := balanceToDust(payerAccount.Balance); got != 8*DustPerCell {
		t.Fatalf("payer after escrow = %d dust, want %d", got, 8*DustPerCell)
	}

	receipt1 := signedUsageReceipt(t, sessionPrivate, StreamUsageReceipt{
		StreamID:                open.StreamID,
		Sequence:                1,
		CumulativeActiveSeconds: 60,
		ObservedAt:              "2026-08-01T00:01:00Z",
	})
	receiptAction1 := StreamAction{
		ID:        "stream-receipt-1",
		StreamID:  open.StreamID,
		Action:    StreamActionReceipt,
		Receipt:   &receipt1,
		Nonce:     0,
		Timestamp: "2026-08-01T00:01:01Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, provider, receiptAction1), accounts); err != nil {
		t.Fatalf("receipt 1: %v", err)
	}
	state, _ := streams.GetStream(open.StreamID)
	wantFirstCharge, _ := streamChargeDust(60, open.PriceDust, open.PricePeriodSeconds)
	if state.AccruedDust != wantFirstCharge {
		t.Fatalf("first accrued = %d, want %d", state.AccruedDust, wantFirstCharge)
	}

	settle := StreamAction{
		ID:        "stream-settle-1",
		StreamID:  open.StreamID,
		Action:    StreamActionSettle,
		Nonce:     1,
		Timestamp: "2026-08-01T00:01:02Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, provider, settle), accounts); err != nil {
		t.Fatalf("settle: %v", err)
	}

	pause := StreamAction{
		ID: "stream-pause", StreamID: open.StreamID, Action: StreamActionPause,
		Nonce: 1, Timestamp: "2026-08-01T00:02:00Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, payer, pause), accounts); err != nil {
		t.Fatalf("pause: %v", err)
	}
	resume := StreamAction{
		ID: "stream-resume", StreamID: open.StreamID, Action: StreamActionResume,
		Nonce: 2, Timestamp: "2026-08-01T00:03:00Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, payer, resume), accounts); err != nil {
		t.Fatalf("resume: %v", err)
	}

	receipt2 := signedUsageReceipt(t, sessionPrivate, StreamUsageReceipt{
		StreamID:                open.StreamID,
		Sequence:                2,
		CumulativeActiveSeconds: 120,
		ObservedAt:              "2026-08-01T00:04:00Z",
	})
	receiptAction2 := StreamAction{
		ID:        "stream-receipt-2",
		StreamID:  open.StreamID,
		Action:    StreamActionReceipt,
		Receipt:   &receipt2,
		Nonce:     2,
		Timestamp: "2026-08-01T00:04:01Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, provider, receiptAction2), accounts); err != nil {
		t.Fatalf("receipt 2: %v", err)
	}

	closeAction := StreamAction{
		ID: "stream-close", StreamID: open.StreamID, Action: StreamActionClose,
		Nonce: 3, Timestamp: "2026-08-01T00:05:00Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, payer, closeAction), accounts); err != nil {
		t.Fatalf("close: %v", err)
	}
	state, _ = streams.GetStream(open.StreamID)
	wantCharge, _ := streamChargeDust(120, open.PriceDust, open.PricePeriodSeconds)
	if state.Status != StreamStatusClosed || state.AccruedDust != wantCharge || state.SettledDust != wantCharge {
		t.Fatalf("closed state = %+v", state)
	}
	if state.PausedDurationSeconds != 60 {
		t.Fatalf("paused duration = %d seconds, want 60", state.PausedDurationSeconds)
	}
	payerAccount, _ = accounts.Get(payer.address)
	providerAccount, _ := accounts.Get(provider.address)
	if got := balanceToDust(payerAccount.Balance); got != 10*DustPerCell-wantCharge {
		t.Fatalf("payer final = %d dust, want %d", got, 10*DustPerCell-wantCharge)
	}
	if got := balanceToDust(providerAccount.Balance); got != wantCharge {
		t.Fatalf("provider final = %d dust, want %d", got, wantCharge)
	}
}

func TestStreamReceiptRejectsReplayAndTampering(t *testing.T) {
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, sessionPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(payer.address, 3)
	streams := NewStreamStateStore()
	open := openStreamAction(t, payer, provider, sessionPublic, DustPerCell)
	if err := streams.ApplyEconomicTx(signedStreamTx(t, payer, open), accounts); err != nil {
		t.Fatal(err)
	}

	receipt := signedUsageReceipt(t, sessionPrivate, StreamUsageReceipt{
		StreamID: open.StreamID, Sequence: 1, CumulativeActiveSeconds: 60,
		ObservedAt: "2026-08-01T00:01:00Z",
	})
	first := StreamAction{
		ID: "receipt-1", StreamID: open.StreamID, Action: StreamActionReceipt,
		Receipt: &receipt, Nonce: 0, Timestamp: "2026-08-01T00:01:01Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, provider, first), accounts); err != nil {
		t.Fatal(err)
	}

	replay := first
	replay.ID = "receipt-replay"
	replay.Nonce = 1
	if err := streams.ApplyEconomicTx(signedStreamTx(t, provider, replay), accounts); !errors.Is(err, ErrStreamReceiptReplay) {
		t.Fatalf("replay error = %v, want ErrStreamReceiptReplay", err)
	}
	tamperedReceipt := receipt
	tamperedReceipt.Sequence = 2
	tamperedReceipt.CumulativeActiveSeconds = 120
	tamperedReceipt.ObservedAt = "2026-08-01T00:02:00Z"
	tampered := StreamAction{
		ID: "receipt-tampered", StreamID: open.StreamID, Action: StreamActionReceipt,
		Receipt: &tamperedReceipt, Nonce: 1, Timestamp: "2026-08-01T00:02:01Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, provider, tampered), accounts); !errors.Is(err, ErrReceiptSignatureInvalid) {
		t.Fatalf("tampered receipt error = %v, want ErrReceiptSignatureInvalid", err)
	}
}

func TestStreamOpenInsufficientBalanceDoesNotCreateState(t *testing.T) {
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(payer.address, 0.5)
	streams := NewStreamStateStore()
	open := openStreamAction(t, payer, provider, sessionPublic, DustPerCell)

	if err := streams.ApplyEconomicTx(signedStreamTx(t, payer, open), accounts); err == nil {
		t.Fatal("open unexpectedly succeeded")
	}
	if streams.Count() != 0 {
		t.Fatalf("failed open left %d streams", streams.Count())
	}
	account, _ := accounts.Get(payer.address)
	if account.Nonce != 0 || balanceToDust(account.Balance) != DustPerCell/2 {
		t.Fatalf("failed open mutated account: %+v", account)
	}
}

func TestStreamSignatureIsReverifiedAtApply(t *testing.T) {
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	open := openStreamAction(t, payer, provider, sessionPublic, DustPerCell)
	tx := signedStreamTx(t, payer, open)
	tx.Signature = hex.EncodeToString(make([]byte, mldsa87.SignatureSize))

	accounts := NewAccountStore()
	accounts.Credit(payer.address, 2)
	err = NewStreamStateStore().ApplyEconomicTx(tx, accounts)
	if !errors.Is(err, ErrStreamSignatureInvalid) {
		t.Fatalf("tampered root signature error = %v", err)
	}
}

func TestEmptyStreamStorePreservesLegacyAwareStateRoot(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit("alice", 1)
	aware := NewEnrollmentAwareApplier(accounts, nil)
	before := aware.StateRoot()
	aware.SetStreamStateStore(NewStreamStateStore())
	if after := aware.StateRoot(); after != before {
		t.Fatalf("empty stream store changed legacy root: before=%s after=%s", before, after)
	}
}

func TestExactMonthlyRateReachesExactlyTwoCELL(t *testing.T) {
	charge, err := streamChargeDust(30*24*60*60, 2*DustPerCell, 30*24*60*60)
	if err != nil {
		t.Fatal(err)
	}
	if charge != 2*DustPerCell {
		t.Fatalf("monthly charge = %d dust, want %d", charge, 2*DustPerCell)
	}
	oneSecond, err := streamChargeDust(1, 2*DustPerCell, 30*24*60*60)
	if err != nil {
		t.Fatal(err)
	}
	if oneSecond != 77 {
		t.Fatalf("first active second = %d dust, want floor 77", oneSecond)
	}
}

func TestStreamReceiptCannotBillFasterThanActiveWallTime(t *testing.T) {
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, sessionPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(payer.address, 3)
	streams := NewStreamStateStore()
	open := openStreamAction(t, payer, provider, sessionPublic, DustPerCell)
	if err := streams.ApplyEconomicTx(signedStreamTx(t, payer, open), accounts); err != nil {
		t.Fatal(err)
	}

	receipt := signedUsageReceipt(t, sessionPrivate, StreamUsageReceipt{
		StreamID:                open.StreamID,
		Sequence:                1,
		CumulativeActiveSeconds: 61,
		ObservedAt:              "2026-08-01T00:01:00Z",
	})
	action := StreamAction{
		ID:        "receipt-too-fast",
		StreamID:  open.StreamID,
		Action:    StreamActionReceipt,
		Receipt:   &receipt,
		Nonce:     0,
		Timestamp: "2026-08-01T00:01:01Z",
	}
	if err := streams.ApplyEconomicTx(signedStreamTx(t, provider, action), accounts); !errors.Is(err, ErrStreamWallTimeExceeded) {
		t.Fatalf("fast receipt error = %v, want ErrStreamWallTimeExceeded", err)
	}
}

func TestStreamActionRequiresCanonicalSpelling(t *testing.T) {
	payer := newStreamTestWallet(t)
	provider := newStreamTestWallet(t)
	sessionPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	open := openStreamAction(t, payer, provider, sessionPublic, DustPerCell)
	nonZeroAmount := signedStreamTx(t, payer, open)
	nonZeroAmount.Amount = 1
	if err := VerifyStreamActionTx(nonZeroAmount); err == nil {
		t.Fatal("stream transaction with an outer amount unexpectedly passed validation")
	}
	nonCanonicalPayload := signedStreamTx(t, payer, open)
	nonCanonicalPayload.Payload = append([]byte(" "), nonCanonicalPayload.Payload...)
	if err := VerifyStreamActionTx(nonCanonicalPayload); err == nil {
		t.Fatal("non-canonical stream payload unexpectedly passed validation")
	}
	unsafeStreamID := open
	unsafeStreamID.StreamID = "vpn/device"
	if err := VerifyStreamActionTx(signedStreamTx(t, payer, unsafeStreamID)); err == nil {
		t.Fatal("path-unsafe stream id unexpectedly passed validation")
	}
	open.Action = "OPEN"
	if err := VerifyStreamActionTx(signedStreamTx(t, payer, open)); err == nil {
		t.Fatal("upper-case stream action unexpectedly passed validation")
	}
}
