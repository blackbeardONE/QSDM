package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

func stakingTestHandlers(t *testing.T) (*Handlers, *wallet.WalletService) {
	t.Helper()
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet service unavailable: %v", err)
	}
	am, _ := NewAuthManager()
	h := NewHandlers(am, NewUserStore(), ws, newMockStorage(),
		logging.NewLogger("test.log", false),
		"", false, 0, "", "", false, 0, false, nil)
	return h, ws
}

// signStakingEnvelope signs the canonical unsigned form, exactly as a
// self-custody client must.
func signStakingEnvelope(t *testing.T, ws *wallet.WalletService, env StakingEnvelope) StakingEnvelope {
	t.Helper()
	unsigned := env
	unsigned.Signature = ""
	unsigned.PublicKey = ""
	canonical, err := json.Marshal(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ws.SignData(canonical)
	if err != nil {
		t.Skipf("wallet cannot sign: %v", err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = hex.EncodeToString(ws.GetPublicKey())
	return env
}

func postStaking(t *testing.T, h *Handlers, env StakingEnvelope) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/staking/submit-signed", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.StakingSubmitSignedHandler(rec, req)
	return rec
}

// TestStakingSubmitSigned_acceptsAValidBond is the end-to-end path that
// makes chain-derived validator membership reachable: without a submission
// endpoint nothing could ever bond, so the staking ledger stayed empty and
// membership always fell back to the node-local bootstrap pair.
func TestStakingSubmitSigned_acceptsAValidBond(t *testing.T) {
	h, ws := stakingTestHandlers(t)
	pool := mempool.New(mempool.DefaultConfig())
	SetStakingMempool(pool)
	t.Cleanup(func() { SetStakingMempool(nil) })

	env := signStakingEnvelope(t, ws, StakingEnvelope{
		ID:        "bond-1",
		Sender:    ws.GetAddress(),
		Action:    chain.StakingActionDelegate,
		Validator: "home-pc",
		Amount:    250,
		Nonce:     1,
		Timestamp: "2026-08-10T00:00:00Z",
	})

	rec := postStaking(t, h, env)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if pool.Size() != 1 {
		t.Fatalf("the bond should reach the mempool, size=%d", pool.Size())
	}
}

// The sender must be bound to the key. A valid signature by some OTHER key
// must not authorise bonding from an account the caller does not control.
func TestStakingSubmitSigned_rejectsSenderImpersonation(t *testing.T) {
	h, ws := stakingTestHandlers(t)
	pool := mempool.New(mempool.DefaultConfig())
	SetStakingMempool(pool)
	t.Cleanup(func() { SetStakingMempool(nil) })

	victim := hex.EncodeToString(sha256Sum([]byte("some-other-public-key")))
	env := signStakingEnvelope(t, ws, StakingEnvelope{
		ID:        "steal-1",
		Sender:    victim, // signed by us, claiming to be someone else
		Action:    chain.StakingActionDelegate,
		Validator: "attacker-node",
		Amount:    500,
		Nonce:     1,
		Timestamp: "2026-08-10T00:00:00Z",
	})

	rec := postStaking(t, h, env)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bonding another account's funds must be refused, got %d: %s",
			rec.Code, rec.Body.String())
	}
	if pool.Size() != 0 {
		t.Fatal("an impersonated bond must never reach the mempool")
	}
}

// Tampering after signing must invalidate the envelope.
func TestStakingSubmitSigned_rejectsTamperedAmount(t *testing.T) {
	h, ws := stakingTestHandlers(t)
	pool := mempool.New(mempool.DefaultConfig())
	SetStakingMempool(pool)
	t.Cleanup(func() { SetStakingMempool(nil) })

	env := signStakingEnvelope(t, ws, StakingEnvelope{
		ID: "bond-2", Sender: ws.GetAddress(), Action: chain.StakingActionDelegate,
		Validator: "home-pc", Amount: 100, Nonce: 1, Timestamp: "2026-08-10T00:00:00Z",
	})
	env.Amount = 1_000_000 // tamper post-signature

	if rec := postStaking(t, h, env); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a tampered amount must fail verification, got %d", rec.Code)
	}
}

// Structural problems are refused before any signature work.
func TestStakingSubmitSigned_rejectsMalformed(t *testing.T) {
	h, ws := stakingTestHandlers(t)
	SetStakingMempool(mempool.New(mempool.DefaultConfig()))
	t.Cleanup(func() { SetStakingMempool(nil) })

	cases := map[string]StakingEnvelope{
		"missing validator": {ID: "a", Sender: ws.GetAddress(), Action: chain.StakingActionDelegate, Amount: 1, Signature: "aa", PublicKey: "bb"},
		"zero amount":       {ID: "a", Sender: ws.GetAddress(), Action: chain.StakingActionDelegate, Validator: "v", Amount: 0, Signature: "aa", PublicKey: "bb"},
		"unknown action":    {ID: "a", Sender: ws.GetAddress(), Action: "drain", Validator: "v", Amount: 1, Signature: "aa", PublicKey: "bb"},
		"missing id":        {Sender: ws.GetAddress(), Action: chain.StakingActionDelegate, Validator: "v", Amount: 1, Signature: "aa", PublicKey: "bb"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := postStaking(t, h, env); rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// With no mempool wired the node must report 503 rather than accept a bond
// it cannot deliver.
func TestStakingSubmitSigned_unconfiguredReports503(t *testing.T) {
	h, ws := stakingTestHandlers(t)
	SetStakingMempool(nil)

	env := signStakingEnvelope(t, ws, StakingEnvelope{
		ID: "bond-3", Sender: ws.GetAddress(), Action: chain.StakingActionDelegate,
		Validator: "home-pc", Amount: 100, Nonce: 1, Timestamp: "2026-08-10T00:00:00Z",
	})
	if rec := postStaking(t, h, env); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when unconfigured, got %d", rec.Code)
	}
	if StakingSubmissionReady() {
		t.Fatal("StakingSubmissionReady must report false with no mempool")
	}
}

// The emitted transaction must carry the bond in the PAYLOAD with tx.Amount
// zero: ApplyStakingTx debits the account itself and refuses a non-zero
// tx.Amount so funds cannot move twice.
func TestStakingMempoolTx_carriesAmountInPayloadNotEnvelope(t *testing.T) {
	tx, err := stakingMempoolTx(StakingEnvelope{
		ID: "x", Sender: "s", Action: chain.StakingActionDelegate,
		Validator: "v", Amount: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx.Amount != 0 {
		t.Fatalf("tx.Amount must be 0 or the applier refuses it, got %v", tx.Amount)
	}
	if tx.ContractID != chain.StakingContractID {
		t.Fatalf("wrong contract id: %s", tx.ContractID)
	}
	p, err := chain.DecodeStakingPayload(tx.Payload)
	if err != nil {
		t.Fatalf("emitted payload must satisfy the chain decoder: %v", err)
	}
	if p.Amount != 42 || p.Validator != "v" {
		t.Fatalf("payload lost the bond details: %+v", p)
	}
}

// Re-submitting the same envelope is idempotent, not a second bond.
func TestStakingSubmitSigned_duplicateIsIdempotent(t *testing.T) {
	h, ws := stakingTestHandlers(t)
	pool := mempool.New(mempool.DefaultConfig())
	SetStakingMempool(pool)
	t.Cleanup(func() { SetStakingMempool(nil) })

	env := signStakingEnvelope(t, ws, StakingEnvelope{
		ID: "bond-dup", Sender: ws.GetAddress(), Action: chain.StakingActionDelegate,
		Validator: "home-pc", Amount: 100, Nonce: 1, Timestamp: "2026-08-10T00:00:00Z",
	})

	if rec := postStaking(t, h, env); rec.Code != http.StatusAccepted {
		t.Fatalf("first submit: %d %s", rec.Code, rec.Body.String())
	}
	rec := postStaking(t, h, env)
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate should be idempotent 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if pool.Size() != 1 {
		t.Fatalf("a duplicate must not bond twice, size=%d", pool.Size())
	}
}

var _ = sha256.Sum256

// The endpoint must be reachable by a self-custody client with no session.
//
// It is authenticated by the envelope signature (sender ==
// hex(sha256(public_key)), checked in the handler), so demanding a JWT or
// CSRF token on top would force a server-side session identity irrelevant
// to the on-chain bond — and would make bonding impossible for exactly the
// clients that need it, defeating the purpose of letting a home node join.
func TestStakingSubmitSigned_isPublicSoSelfCustodyClientsCanBond(t *testing.T) {
	if !isPublicEndpoint("/api/v1/staking/submit-signed") {
		t.Fatal("staking submission must be public: it is self-authenticating, " +
			"and requiring CSRF/JWT blocks the self-custody clients it exists for")
	}
	// Sanity: the sibling self-custody endpoint has the same posture.
	if !isPublicEndpoint("/api/v1/wallet/submit-signed") {
		t.Fatal("precondition: /wallet/submit-signed should already be public")
	}
}
