package chain

import (
	"encoding/hex"
	"errors"
	"math/big"
	mrand "math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// qsdm20Rig is a block applier with the token store wired, the activation
// height set and a settable block height, as production wires it.
type qsdm20Rig struct {
	t        *testing.T
	accounts *AccountStore
	aware    *EnrollmentAwareApplier
	tokens   *QSDM20StateStore
	height   uint64
}

func newQSDM20Rig(t *testing.T, activation uint64) *qsdm20Rig {
	t.Helper()
	setQSDM20ActivationForTest(t, activation)
	r := &qsdm20Rig{t: t, accounts: NewAccountStore(), tokens: NewQSDM20StateStore(), height: activation}
	if r.height == 0 {
		r.height = 1
	}
	r.aware = NewEnrollmentAwareApplier(r.accounts, nil)
	r.aware.SetTaskStateStore(NewTaskStateStore())
	r.aware.SetStreamStateStore(NewStreamStateStore())
	r.aware.SetRecoveryCapsuleStateStore(NewRecoveryCapsuleStateStore())
	r.aware.SetQSDM20StateStore(r.tokens)
	r.aware.SetHeightFn(func() uint64 { return r.height })
	return r
}

func (r *qsdm20Rig) wallet(cell float64) qsdm20Wallet {
	r.t.Helper()
	w := newQSDM20Wallet(r.t)
	if cell > 0 {
		r.accounts.Credit(w.address, cell)
	}
	return w
}

func (r *qsdm20Rig) nonce(w qsdm20Wallet) uint64 {
	acc, ok := r.accounts.Get(w.address)
	if !ok {
		return 0
	}
	return acc.Nonce
}

// tx signs a with w's current account nonce.
func (r *qsdm20Rig) tx(w qsdm20Wallet, feeDust uint64, a QSDM20Action) *mempool.Tx {
	r.t.Helper()
	return w.signedTx(r.t, r.nonce(w), feeDust, a)
}

func (r *qsdm20Rig) mustApply(tx *mempool.Tx) {
	r.t.Helper()
	if err := r.aware.ApplyTx(tx); err != nil {
		r.t.Fatalf("apply %s: %v", tx.ID, err)
	}
}

// do applies a from w at its current nonce and must succeed.
func (r *qsdm20Rig) do(w qsdm20Wallet, a QSDM20Action) *mempool.Tx {
	r.t.Helper()
	tx := r.tx(w, 0, a)
	r.mustApply(tx)
	return tx
}

// create makes a token from w and returns its id.
func (r *qsdm20Rig) create(w qsdm20Wallet, a QSDM20Action) string {
	r.t.Helper()
	id := QSDM20TokenID(w.address, r.nonce(w))
	r.do(w, a)
	return id
}

type qsdm20Snapshot struct {
	root      string
	tokenRoot string
	accounts  string
}

func (r *qsdm20Rig) snapshot() qsdm20Snapshot {
	var b strings.Builder
	for _, acc := range r.accounts.AllAccounts() {
		b.WriteString(acc.Address + ":" + strconv.FormatFloat(acc.Balance, 'g', -1, 64) + ":" + strconv.FormatUint(acc.Nonce, 10) + ";")
	}
	return qsdm20Snapshot{root: r.aware.StateRoot(), tokenRoot: r.tokens.StateRoot(), accounts: b.String()}
}

// refused applies tx, which must fail with want and change nothing: no
// fee, no nonce, no token state, no root.
func (r *qsdm20Rig) refused(tx *mempool.Tx, want error) {
	r.t.Helper()
	before := r.snapshot()
	err := r.aware.ApplyTx(tx)
	if err == nil {
		r.t.Fatalf("transaction %s was applied, want %v", tx.ID, want)
	}
	if want != nil && !errors.Is(err, want) {
		r.t.Fatalf("got %v, want %v", err, want)
	}
	if after := r.snapshot(); after != before {
		r.t.Fatalf("refused transaction changed state:\nbefore %+v\nafter  %+v", before, after)
	}
}

func (r *qsdm20Rig) balance(id, address string) string {
	r.t.Helper()
	for _, b := range r.tokens.BalancesOf(address) {
		if b.Token == id {
			return b.Balance
		}
	}
	return "0"
}

func (r *qsdm20Rig) token(id string) QSDM20Token {
	r.t.Helper()
	tok, ok := r.tokens.Token(id)
	if !ok {
		r.t.Fatalf("token %s missing", id)
	}
	return tok
}

func TestQSDM20CreateFixedSupplyChargesFeeAndNonce(t *testing.T) {
	r := newQSDM20Rig(t, 10)
	r.height = 12
	issuer := r.wallet(5)
	tx := issuer.signedTx(t, 0, 50_000_000, qsdm20Create("GROOT", "Groot Coin", 8, "1000000", "1000000", QSDM20MintNone, ""))
	r.mustApply(tx)
	id := QSDM20TokenID(issuer.address, 0)
	tok := r.token(id)
	want := QSDM20Token{
		ID: id, Symbol: "GROOT", Name: "Groot Coin", Decimals: 8, Issuer: issuer.address, Mint: QSDM20MintNone,
		MaxSupply: "1000000", Minted: "1000000", Burned: "0", Supply: "1000000", Holders: 1, CreatedHeight: 12,
	}
	if tok != want {
		t.Fatalf("token = %+v\nwant    %+v", tok, want)
	}
	if got := r.balance(id, issuer.address); got != "1000000" {
		t.Fatalf("issuer balance = %s", got)
	}
	acc, _ := r.accounts.Get(issuer.address)
	if acc.Nonce != 1 || acc.Balance != 4.5 {
		t.Fatalf("fee and nonce: %+v (want balance 4.5, nonce 1)", acc)
	}
	if r.tokens.Count() != 1 {
		t.Fatalf("count = %d", r.tokens.Count())
	}
}

func TestQSDM20TransferAndInsufficientUnits(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice, bob := r.wallet(1), r.wallet(1)
	carol := newQSDM20Wallet(t) // no CELL account: may still receive tokens
	id := r.create(alice, qsdm20Create("TT", "Transfer test", 0, "100", "100", QSDM20MintNone, ""))

	r.do(alice, qsdm20Transfer(id, bob.address, "30"))
	r.do(bob, qsdm20Transfer(id, carol.address, "10"))
	if a, b, c := r.balance(id, alice.address), r.balance(id, bob.address), r.balance(id, carol.address); a != "70" || b != "20" || c != "10" {
		t.Fatalf("balances alice %s bob %s carol %s", a, b, c)
	}
	// All of bob's units: bob stops being a holder.
	r.do(bob, qsdm20Transfer(id, alice.address, "20"))
	if r.token(id).Holders != 2 || r.balance(id, bob.address) != "0" {
		t.Fatalf("emptied holder kept: %+v", r.token(id))
	}
	// A transfer to oneself moves nothing and is allowed.
	r.do(alice, qsdm20Transfer(id, alice.address, "90"))
	if r.balance(id, alice.address) != "90" {
		t.Fatalf("self transfer changed the balance: %s", r.balance(id, alice.address))
	}

	r.refused(r.tx(alice, 0, qsdm20Transfer(id, bob.address, "91")), ErrQSDM20InsufficientUnits)
	r.refused(r.tx(bob, 0, qsdm20Transfer(id, alice.address, "1")), ErrQSDM20InsufficientUnits)
	r.refused(r.tx(alice, 0, qsdm20Transfer(id, alice.address, "91")), ErrQSDM20InsufficientUnits)
	if s := r.token(id).Supply; s != "100" {
		t.Fatalf("supply moved: %s", s)
	}
}

func TestQSDM20UnknownTokenRefused(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice, bob := r.wallet(1), r.wallet(1)
	unknown := QSDM20TokenID(bob.address, 99)
	r.refused(r.tx(alice, 0, qsdm20Transfer(unknown, bob.address, "1")), ErrQSDM20UnknownToken)
	r.refused(r.tx(alice, 0, qsdm20MintAction(unknown, alice.address, "1")), ErrQSDM20UnknownToken)
	r.refused(r.tx(alice, 0, qsdm20Burn(unknown, "1")), ErrQSDM20UnknownToken)
	r.refused(r.tx(alice, 0, qsdm20Renounce(unknown)), ErrQSDM20UnknownToken)
}

func TestQSDM20IssuerMint(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	issuer, other := r.wallet(1), r.wallet(1)
	id := r.create(issuer, qsdm20Create("ISS", "Issuer coin", 2, "1000", "100", QSDM20MintIssuer, ""))

	r.do(issuer, qsdm20MintAction(id, other.address, "400"))
	if b := r.balance(id, other.address); b != "400" {
		t.Fatalf("minted to other: %s", b)
	}
	r.refused(r.tx(other, 0, qsdm20MintAction(id, other.address, "1")), ErrQSDM20NotIssuer)
	r.refused(r.tx(issuer, 0, qsdm20MintAction(id, issuer.address, "501")), ErrQSDM20OverMaxSupply)
	r.do(issuer, qsdm20MintAction(id, issuer.address, "500")) // exactly up to max_supply
	tok := r.token(id)
	if tok.Minted != "1000" || tok.Supply != "1000" || tok.MaxSupply != "1000" {
		t.Fatalf("after minting to max: %+v", tok)
	}
	r.refused(r.tx(issuer, 0, qsdm20MintAction(id, issuer.address, "1")), ErrQSDM20OverMaxSupply)
}

func TestQSDM20MintRefusedForFixedSupply(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	issuer := r.wallet(1)
	id := r.create(issuer, qsdm20Create("FIX", "Fixed", 0, "5", "5", QSDM20MintNone, ""))
	r.refused(r.tx(issuer, 0, qsdm20MintAction(id, issuer.address, "1")), ErrQSDM20MintingOff)
}

func TestQSDM20OpenMint(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	issuer, minter, other := r.wallet(1), r.wallet(1), r.wallet(1)
	id := r.create(issuer, qsdm20Create("FAIR", "Fair launch", 0, "250", "0", QSDM20MintOpen, "100"))
	if tok := r.token(id); tok.Minted != "0" || tok.Holders != 0 || tok.MintLimit != "100" {
		t.Fatalf("open token with no initial supply: %+v", tok)
	}

	r.do(minter, qsdm20MintAction(id, minter.address, "100")) // exactly mint_limit
	r.do(issuer, qsdm20MintAction(id, issuer.address, "50"))  // the issuer mints like anyone
	r.refused(r.tx(minter, 0, qsdm20MintAction(id, minter.address, "101")), ErrQSDM20OverMintLimit)
	r.refused(r.tx(minter, 0, qsdm20MintAction(id, other.address, "1")), ErrQSDM20OpenMintRecipient)
	r.refused(r.tx(issuer, 0, qsdm20MintAction(id, other.address, "1")), ErrQSDM20OpenMintRecipient)
	r.do(other, qsdm20MintAction(id, other.address, "100")) // minted 250 = max
	r.refused(r.tx(other, 0, qsdm20MintAction(id, other.address, "1")), ErrQSDM20OverMaxSupply)
	tok := r.token(id)
	if tok.Minted != "250" || tok.Holders != 3 {
		t.Fatalf("after the fair launch: %+v", tok)
	}
}

// Burned units are gone: a mint is allowed only while minted + amount <=
// max_supply, so burning never makes room for new units.
func TestQSDM20BurnedUnitsCannotBeReminted(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	issuer := r.wallet(1)
	id := r.create(issuer, qsdm20Create("BRN", "Burn", 0, "100", "100", QSDM20MintIssuer, ""))
	r.do(issuer, qsdm20Burn(id, "40"))
	tok := r.token(id)
	if tok.Minted != "100" || tok.Burned != "40" || tok.Supply != "60" || r.balance(id, issuer.address) != "60" {
		t.Fatalf("after burn: %+v balance %s", tok, r.balance(id, issuer.address))
	}
	r.refused(r.tx(issuer, 0, qsdm20MintAction(id, issuer.address, "1")), ErrQSDM20OverMaxSupply)
	r.refused(r.tx(issuer, 0, qsdm20Burn(id, "61")), ErrQSDM20InsufficientUnits)
	r.do(issuer, qsdm20Burn(id, "60"))
	if tok := r.token(id); tok.Supply != "0" || tok.Holders != 0 || tok.Burned != "100" {
		t.Fatalf("after burning everything: %+v", tok)
	}
	other := r.wallet(1)
	r.refused(r.tx(other, 0, qsdm20Burn(id, "1")), ErrQSDM20InsufficientUnits)
}

func TestQSDM20Renounce(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	issuer, other := r.wallet(1), r.wallet(1)
	id := r.create(issuer, qsdm20Create("REN", "Renounce", 0, "1000", "10", QSDM20MintIssuer, ""))
	r.do(issuer, qsdm20MintAction(id, other.address, "15"))
	r.do(other, qsdm20Burn(id, "5"))

	r.refused(r.tx(other, 0, qsdm20Renounce(id)), ErrQSDM20NotIssuer)
	r.do(issuer, qsdm20Renounce(id))
	tok := r.token(id)
	if tok.Mint != QSDM20MintNone || tok.MaxSupply != "25" || tok.Minted != "25" || tok.Burned != "5" || tok.Supply != "20" {
		t.Fatalf("after renounce: %+v (max_supply must become the 25 minted so far)", tok)
	}
	r.refused(r.tx(issuer, 0, qsdm20MintAction(id, issuer.address, "1")), ErrQSDM20MintingOff)
	r.refused(r.tx(issuer, 0, qsdm20Renounce(id)), ErrQSDM20MintingOff)

	fixed := r.create(issuer, qsdm20Create("FIX", "Fixed", 0, "3", "3", QSDM20MintNone, ""))
	r.refused(r.tx(issuer, 0, qsdm20Renounce(fixed)), ErrQSDM20MintingOff)

	open := r.create(issuer, qsdm20Create("OPN", "Open", 0, "1000", "0", QSDM20MintOpen, "10"))
	r.do(other, qsdm20MintAction(open, other.address, "7"))
	r.refused(r.tx(other, 0, qsdm20Renounce(open)), ErrQSDM20NotIssuer)
	r.do(issuer, qsdm20Renounce(open))
	if tok := r.token(open); tok.Mint != QSDM20MintNone || tok.MaxSupply != "7" || tok.MintLimit != "" {
		t.Fatalf("renounced open token: %+v", tok)
	}
	r.refused(r.tx(other, 0, qsdm20MintAction(open, other.address, "1")), ErrQSDM20MintingOff)
}

// The CELL side: the sender needs an account with the fee, and the fee is
// burned (debited, credited to nobody).
func TestQSDM20FeeAndAccountRefusals(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	poor := r.wallet(0.5)
	create := qsdm20Create("FEE", "Fee", 0, "1", "1", QSDM20MintNone, "")
	r.refused(poor.signedTx(t, 0, 50_000_001, create), nil) // 0.50000001 CELL > 0.5
	if r.tokens.Count() != 0 {
		t.Fatal("refused create made a token")
	}
	tx := poor.signedTx(t, 0, 50_000_000, create)
	r.mustApply(tx)
	if acc, _ := r.accounts.Get(poor.address); acc.Balance != 0 || acc.Nonce != 1 {
		t.Fatalf("fee not burned: %+v", acc)
	}
	var total float64
	for _, acc := range r.accounts.AllAccounts() {
		total += acc.Balance
	}
	if total != 0 {
		t.Fatalf("the fee went somewhere: total CELL %v", total)
	}

	stranger := newQSDM20Wallet(t) // no CELL account
	r.refused(stranger.signedTx(t, 0, 0, qsdm20Create("NOA", "No account", 0, "1", "1", QSDM20MintNone, "")), nil)
	if _, ok := r.accounts.Get(stranger.address); ok {
		t.Fatal("a refused transaction created an account")
	}
}

func TestQSDM20ReplayedNonceRefused(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice, bob := r.wallet(1), r.wallet(1)
	create := r.tx(alice, 0, qsdm20Create("RPL", "Replay", 0, "100", "100", QSDM20MintNone, ""))
	r.mustApply(create)
	id := QSDM20TokenID(alice.address, 0)
	r.refused(create, ErrQSDM20TokenExists) // the same create again

	transfer := r.tx(alice, 0, qsdm20Transfer(id, bob.address, "10"))
	r.mustApply(transfer)
	r.refused(transfer, nil) // nonce 1 is used
	if r.balance(id, bob.address) != "10" {
		t.Fatalf("replayed transfer moved units: %s", r.balance(id, bob.address))
	}
	// A different transaction at a used nonce is refused too.
	r.refused(alice.signedTx(t, 1, 0, qsdm20Transfer(id, bob.address, "11")), nil)
	// And one at a future nonce.
	r.refused(alice.signedTx(t, 5, 0, qsdm20Transfer(id, bob.address, "1")), nil)
}

func TestQSDM20BelowActivationRefused(t *testing.T) {
	r := newQSDM20Rig(t, 100)
	alice := r.wallet(1)
	create := qsdm20Create("ACT", "Activation", 0, "1", "1", QSDM20MintNone, "")
	r.height = 99
	r.refused(r.tx(alice, 0, create), ErrQSDM20Inactive)
	if err := NewQSDM20StateStore().ApplyHistoricalTx(r.tx(alice, 0, create), 99); !errors.Is(err, ErrQSDM20Inactive) {
		t.Fatalf("historical replay below the height: %v", err)
	}
	r.height = 100
	tx := r.tx(alice, 0, create)
	r.mustApply(tx)
	if err := NewQSDM20StateStore().ApplyHistoricalTx(tx, 100); err != nil {
		t.Fatalf("historical replay at the height: %v", err)
	}
}

// Activation height 0 (the default): every token transaction is refused,
// with or without a height source, and never reaches the plain transfer
// path (which would bump the nonce and charge the fee).
func TestQSDM20SwitchedOffRefusesEverywhere(t *testing.T) {
	r := newQSDM20Rig(t, 0)
	alice := r.wallet(2)
	tx := r.tx(alice, 10_000_000, qsdm20Create("OFF", "Off", 0, "1", "1", QSDM20MintNone, ""))
	for _, h := range []uint64{0, 1, 1 << 40} {
		r.height = h
		r.refused(tx, ErrQSDM20Inactive)
	}
	r.aware.SetHeightFn(nil)
	r.refused(tx, ErrQSDM20Inactive)
	for _, h := range []uint64{0, 1, 1 << 40} {
		if err := NewQSDM20StateStore().ApplyHistoricalTx(tx, h); !errors.Is(err, ErrQSDM20Inactive) {
			t.Fatalf("historical replay at %d: %v", h, err)
		}
	}
	if err := r.tokens.ApplyTx(tx); !errors.Is(err, ErrQSDM20Inactive) {
		t.Fatalf("store ApplyTx: %v", err)
	}
	if err := r.aware.CheckQSDM20Tx(tx); !errors.Is(err, ErrQSDM20Inactive) {
		t.Fatalf("CheckQSDM20Tx: %v", err)
	}
	if acc, _ := r.accounts.Get(alice.address); acc.Nonce != 0 || acc.Balance != 2 {
		t.Fatalf("switched-off token tx touched the account: %+v", acc)
	}
}

func TestQSDM20NoHeightSourceFailsClosed(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice := r.wallet(1)
	r.aware.SetHeightFn(nil)
	r.refused(r.tx(alice, 0, qsdm20Create("NHS", "No height", 0, "1", "1", QSDM20MintNone, "")), ErrEnrollmentHeightUnset)
}

func TestQSDM20StoreNotWiredRefused(t *testing.T) {
	setQSDM20ActivationForTest(t, 1)
	accounts := NewAccountStore()
	alice := newQSDM20Wallet(t)
	accounts.Credit(alice.address, 1)
	aware := NewEnrollmentAwareApplier(accounts, nil)
	aware.SetHeightFn(func() uint64 { return 5 })
	tx := alice.signedTx(t, 0, 0, qsdm20Create("NW", "Not wired", 0, "1", "1", QSDM20MintNone, ""))
	if err := aware.ApplyTx(tx); !errors.Is(err, ErrQSDM20StateNotWired) {
		t.Fatalf("got %v", err)
	}
	if err := aware.CheckQSDM20Tx(tx); !errors.Is(err, ErrQSDM20StateNotWired) {
		t.Fatalf("check: %v", err)
	}
	if acc, _ := accounts.Get(alice.address); acc.Nonce != 0 {
		t.Fatal("nonce moved")
	}
}

// A wrong public key or a tampered signature is refused when the block is
// applied, not only at admission.
func TestQSDM20SignatureRefusedAtApply(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice, mallory := r.wallet(1), r.wallet(1)
	id := r.create(alice, qsdm20Create("SIG", "Signature", 0, "100", "100", QSDM20MintNone, ""))

	// Mallory signs the exact digest of a transfer of alice's units with her
	// own key: her key does not hash to alice's address.
	steal := qsdm20Transfer(id, mallory.address, "100")
	forged := qsdm20UnsignedTx(t, alice.address, r.nonce(alice), 0, steal)
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(mallory.private, QSDM20SigningDigest(alice.address, r.nonce(alice), 0, steal), nil, true, sig); err != nil {
		t.Fatal(err)
	}
	forged.Signature, forged.PublicKey = hex.EncodeToString(sig), hex.EncodeToString(mallory.pub)
	r.refused(forged, ErrQSDM20Signature)

	tampered := r.tx(alice, 0, qsdm20Transfer(id, mallory.address, "1"))
	raw, _ := hex.DecodeString(tampered.Signature)
	raw[0] ^= 0x80
	tampered.Signature = hex.EncodeToString(raw)
	r.refused(tampered, ErrQSDM20Signature)
	if r.balance(id, mallory.address) != "0" {
		t.Fatal("units moved")
	}
}

// CheckQSDM20Tx (the submit API's check) agrees with the applier and
// changes nothing.
func TestQSDM20CheckMatchesApply(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice, bob := r.wallet(1), r.wallet(1)
	id := r.create(alice, qsdm20Create("CHK", "Check", 0, "10", "10", QSDM20MintNone, ""))
	ok := r.tx(alice, 0, qsdm20Transfer(id, bob.address, "10"))
	tooMuch := r.tx(alice, 0, qsdm20Transfer(id, bob.address, "11"))
	stale := alice.signedTx(t, 0, 0, qsdm20Transfer(id, bob.address, "1"))
	before := r.snapshot()
	if err := r.aware.CheckQSDM20Tx(ok); err != nil {
		t.Fatalf("check of a valid transfer: %v", err)
	}
	if err := r.aware.CheckQSDM20Tx(tooMuch); !errors.Is(err, ErrQSDM20InsufficientUnits) {
		t.Fatalf("check of an overdraft: %v", err)
	}
	if err := r.aware.CheckQSDM20Tx(stale); err == nil || !strings.Contains(err.Error(), "nonce mismatch") {
		t.Fatalf("check of a stale nonce: %v", err)
	}
	if after := r.snapshot(); after != before {
		t.Fatal("CheckQSDM20Tx changed state")
	}
	r.mustApply(ok)
}

// Whatever happens, a token's balances add up to minted - burned, and
// minted never passes max_supply.
func TestQSDM20SupplyInvariant(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	rng := mrand.New(mrand.NewSource(20))
	wallets := []qsdm20Wallet{r.wallet(1), r.wallet(1), r.wallet(1)}
	issuer := wallets[0]
	ids := []string{
		r.create(issuer, qsdm20Create("INV", "Issuer", 0, "1000", "100", QSDM20MintIssuer, "")),
		r.create(wallets[1], qsdm20Create("OPN", "Open", 0, "700", "0", QSDM20MintOpen, "40")),
	}
	for i := 0; i < 120; i++ {
		w := wallets[rng.Intn(len(wallets))]
		to := wallets[rng.Intn(len(wallets))]
		id := ids[rng.Intn(len(ids))]
		amount := strconv.Itoa(1 + rng.Intn(60))
		var a QSDM20Action
		switch rng.Intn(4) {
		case 0:
			a = qsdm20Transfer(id, to.address, amount)
		case 1:
			a = qsdm20MintAction(id, to.address, amount)
		case 2:
			a = qsdm20Burn(id, amount)
		default:
			a = qsdm20MintAction(id, w.address, amount)
		}
		_ = r.aware.ApplyTx(r.tx(w, 0, a))
		for _, id := range ids {
			tok := r.token(id)
			holders, _ := r.tokens.Holders(id)
			sum := new(big.Int)
			for _, h := range holders {
				b, _ := new(big.Int).SetString(h.Balance, 10)
				if b.Sign() <= 0 {
					t.Fatalf("holder %s with balance %s", h.Address, h.Balance)
				}
				sum.Add(sum, b)
			}
			minted, _ := new(big.Int).SetString(tok.Minted, 10)
			burned, _ := new(big.Int).SetString(tok.Burned, 10)
			maxSupply, _ := new(big.Int).SetString(tok.MaxSupply, 10)
			if sum.Cmp(new(big.Int).Sub(minted, burned)) != 0 || sum.String() != tok.Supply {
				t.Fatalf("step %d: balances %s != minted %s - burned %s", i, sum, minted, burned)
			}
			if minted.Cmp(maxSupply) > 0 {
				t.Fatalf("step %d: minted %s > max_supply %s", i, minted, maxSupply)
			}
		}
	}
}

// Amounts are exact up to 2^256 - 1.
func TestQSDM20LargestAmounts(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice, bob := r.wallet(1), r.wallet(1)
	id := r.create(alice, qsdm20Create("MAX", "Max", 18, qsdm20MaxUnitsString, "1", QSDM20MintIssuer, ""))
	rest := new(big.Int).Sub(qsdm20MaxUnits, big.NewInt(1)).String()
	r.do(alice, qsdm20MintAction(id, bob.address, rest))
	if tok := r.token(id); tok.Minted != qsdm20MaxUnitsString || tok.Supply != qsdm20MaxUnitsString {
		t.Fatalf("minted to 2^256-1: %+v", tok)
	}
	r.refused(r.tx(alice, 0, qsdm20MintAction(id, bob.address, "1")), ErrQSDM20OverMaxSupply)
	r.do(alice, qsdm20Transfer(id, bob.address, "1"))
	if b := r.balance(id, bob.address); b != qsdm20MaxUnitsString {
		t.Fatalf("bob holds %s", b)
	}
}

func TestQSDM20ReadViews(t *testing.T) {
	r := newQSDM20Rig(t, 1)
	alice, bob, carol := r.wallet(1), r.wallet(1), r.wallet(1)
	r.height = 5
	first := r.create(alice, qsdm20Create("ONE", "First", 0, "100", "100", QSDM20MintNone, ""))
	r.height = 9
	second := r.create(bob, qsdm20Create("TWO", "Second", 0, "100", "100", QSDM20MintNone, ""))
	third := r.create(carol, qsdm20Create("THREE", "Third", 0, "100", "100", QSDM20MintNone, ""))

	list := r.tokens.Tokens()
	if len(list) != 3 || list[2].ID != first {
		t.Fatalf("tokens not newest first: %+v", list)
	}
	sameHeight := []string{second, third}
	if sameHeight[0] > sameHeight[1] {
		sameHeight[0], sameHeight[1] = sameHeight[1], sameHeight[0]
	}
	if list[0].ID != sameHeight[0] || list[1].ID != sameHeight[1] {
		t.Fatal("tokens of one height must be ordered by id")
	}

	r.do(alice, qsdm20Transfer(first, carol.address, "30"))
	r.do(alice, qsdm20Transfer(first, bob.address, "30"))
	holders, ok := r.tokens.Holders(first)
	if !ok || len(holders) != 3 || holders[0].Address != alice.address || holders[0].Balance != "40" {
		t.Fatalf("holders: %+v", holders)
	}
	tied := []string{bob.address, carol.address}
	if tied[0] > tied[1] {
		tied[0], tied[1] = tied[1], tied[0]
	}
	if holders[1].Address != tied[0] || holders[2].Address != tied[1] {
		t.Fatalf("equal balances must be ordered by address: %+v", holders)
	}
	if _, ok := r.tokens.Holders(QSDM20TokenID(alice.address, 77)); ok {
		t.Fatal("holders of an unknown token")
	}
	balances := r.tokens.BalancesOf(carol.address)
	if len(balances) != 2 || balances[0].Token > balances[1].Token {
		t.Fatalf("carol's balances: %+v", balances)
	}
	if got := r.tokens.BalancesOf(strings.Repeat("0", 64)); len(got) != 0 {
		t.Fatalf("balances of a stranger: %+v", got)
	}
}
