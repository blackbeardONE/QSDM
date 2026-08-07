package api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/walletrecovery"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func testRecoveryCapsuleEnvelope(t *testing.T, nonce uint64) QSDMRecoveryCapsuleEnvelope {
	t.Helper()
	public, private, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, _ := public.MarshalBinary()
	privateBytes, _ := private.MarshalBinary()
	addressHash := sha256.Sum256(publicBytes)
	address := hex.EncodeToString(addressHash[:])
	material, err := walletrecovery.GenerateLegacyCapsule(address, publicBytes, privateBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer material.ZeroSecrets()
	action := chain.RecoveryCapsuleAction{
		ID:        "api-recovery-register",
		Sender:    address,
		Action:    chain.RecoveryCapsuleActionRegister,
		Locator:   material.Capsule.Locator,
		Capsule:   material.Capsule,
		Nonce:     nonce,
		Timestamp: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano),
	}
	message, err := chain.RecoveryCapsuleActionSigningBytes(action)
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(private, message, nil, true, signature); err != nil {
		t.Fatal(err)
	}
	return QSDMRecoveryCapsuleEnvelope{
		Action:    action,
		Signature: hex.EncodeToString(signature),
		PublicKey: hex.EncodeToString(publicBytes),
	}
}

func postRecoveryCapsule(t *testing.T, env QSDMRecoveryCapsuleEnvelope) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/recovery/capsules/submit-signed", bytes.NewReader(raw))
	(&Handlers{}).QSDMRecoveryCapsuleSubmitSignedHandler(rec, req)
	return rec
}

func TestRecoveryCapsuleSubmitAndRead(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	SetRecoveryCapsuleMempool(pool)
	t.Cleanup(func() { SetRecoveryCapsuleMempool(nil) })
	env := testRecoveryCapsuleEnvelope(t, 3)
	SetMiningAccountProbe(&fakeAccountProbe{addrs: map[string]struct {
		bal   float64
		nonce uint64
	}{env.Action.Sender: {bal: 1, nonce: 3}}})
	t.Cleanup(func() { SetMiningAccountProbe(nil) })

	rec := postRecoveryCapsule(t, env)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", rec.Code, rec.Body.String())
	}
	tx, ok := pool.Get(env.Action.ID)
	if !ok {
		t.Fatal("accepted recovery capsule did not reach the mempool")
	}
	store := chain.NewRecoveryCapsuleStateStore()
	if err := store.ApplyHistoricalTx(tx, 10); err != nil {
		t.Fatal(err)
	}
	SetRecoveryCapsuleStateProvider(store)
	t.Cleanup(func() { SetRecoveryCapsuleStateProvider(nil) })

	byOwner := httptest.NewRecorder()
	(&Handlers{}).QSDMRecoveryCapsuleLookupHandler(byOwner, httptest.NewRequest(
		http.MethodGet, "/api/v1/wallet/recovery/capsules?owner="+env.Action.Sender, nil))
	if byOwner.Code != http.StatusOK {
		t.Fatalf("owner lookup status=%d body=%s", byOwner.Code, byOwner.Body.String())
	}
	byLocator := httptest.NewRecorder()
	(&Handlers{}).QSDMRecoveryCapsuleRouteHandler(byLocator, httptest.NewRequest(
		http.MethodGet, "/api/v1/wallet/recovery/capsules/"+env.Action.Locator, nil))
	if byLocator.Code != http.StatusOK {
		t.Fatalf("locator lookup status=%d body=%s", byLocator.Code, byLocator.Body.String())
	}
}

func TestRecoveryCapsuleSubmitRejectsTamperAndUnknownWallet(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	SetRecoveryCapsuleMempool(pool)
	t.Cleanup(func() { SetRecoveryCapsuleMempool(nil) })
	env := testRecoveryCapsuleEnvelope(t, 0)
	SetMiningAccountProbe(&fakeAccountProbe{addrs: map[string]struct {
		bal   float64
		nonce uint64
	}{}})
	t.Cleanup(func() { SetMiningAccountProbe(nil) })
	if rec := postRecoveryCapsule(t, env); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown wallet status=%d body=%s", rec.Code, rec.Body.String())
	}

	SetMiningAccountProbe(&fakeAccountProbe{addrs: map[string]struct {
		bal   float64
		nonce uint64
	}{env.Action.Sender: {bal: 1, nonce: 0}}})
	env.Action.Capsule.Ciphertext = env.Action.Capsule.Ciphertext[:len(env.Action.Capsule.Ciphertext)-4] + "AAAA"
	if rec := postRecoveryCapsule(t, env); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("tampered capsule status=%d body=%s", rec.Code, rec.Body.String())
	}
	if pool.Size() != 0 {
		t.Fatal("rejected recovery capsule reached mempool")
	}
}

func TestRecoveryCapsuleEndpointsArePublic(t *testing.T) {
	for _, path := range []string{
		"/api/v1/wallet/recovery/nonce",
		"/api/v1/wallet/recovery/capsules",
		"/api/v1/wallet/recovery/capsules/submit-signed",
		"/api/v1/wallet/recovery/capsules/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		if !isPublicEndpoint(path) {
			t.Fatalf("%s should be public", path)
		}
	}
}
