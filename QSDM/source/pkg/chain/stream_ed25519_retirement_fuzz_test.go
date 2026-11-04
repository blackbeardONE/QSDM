package chain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// FuzzStreamEd25519Retirement applies one qsdm/streams/v1 transaction from a
// block at height h, under the stream Ed25519 retirement height H, through
// both paths the rule guards: the block applier (EnrollmentAwareApplier.ApplyTx)
// and the boot replay of the stream projection
// (StreamStateStore.ApplyHistoricalTx). Each path runs twice on identical
// state that already holds an open stream: once with the rule off and once
// with it on. In "harness signs" mode the decoded action is re-encoded and
// signed by the funded payer (by the provider for receipts and settlements),
// so the fuzzer reaches the stream state machine; otherwise the payload is
// sent as is under the payer's key with a zero signature.
//
// The rule may change exactly one outcome: with H set, a stream open in a
// block at or above H is refused with ErrStreamEd25519SessionKeyRetired (by
// the applier, with ErrEnrollmentHeightUnset when it has no height source)
// and changes no state. Every other transaction, and every transaction below
// H, has the same outcome and the same state root as with the rule off.
func FuzzStreamEd25519Retirement(f *testing.F) {
	payer, provider := newFuzzKey(0x51), newFuzzKey(0x52)
	sessionPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x53}, ed25519.SeedSize))
	sessionPublic := sessionPrivate.Public().(ed25519.PublicKey)
	device := sha256.Sum256([]byte("fuzz-device"))
	open := func(id, streamID string, nonce uint64) StreamAction {
		return StreamAction{
			ID: id, Sender: payer.address, StreamID: streamID, Action: StreamActionOpen, Provider: provider.address,
			ServiceID: "qsdm-vpn", DeviceIDHash: hex.EncodeToString(device[:]),
			SessionPublicKey: hex.EncodeToString(sessionPublic), PriceDust: 2 * DustPerCell,
			PricePeriodSeconds: 30 * 24 * 60 * 60, BudgetDust: 2 * DustPerCell,
			MaxActiveSeconds: 30 * 24 * 60 * 60, ExpiresAt: "2026-09-01T00:00:00Z",
			Nonce: nonce, Timestamp: "2026-08-01T00:00:00Z",
		}
	}
	sign := func(tb testing.TB, key fuzzKey, a StreamAction) *mempool.Tx {
		tb.Helper()
		a.Sender = key.address
		payload, err := json.Marshal(a)
		if err != nil {
			tb.Fatal(err)
		}
		return &mempool.Tx{ID: a.ID, Sender: a.Sender, Nonce: a.Nonce, Payload: payload,
			ContractID: StreamContractID, PublicKey: hex.EncodeToString(key.pub),
			Signature: hex.EncodeToString(key.mustSign(tb, payload))}
	}
	zeroSignature := strings.Repeat("00", mldsa87.SignatureSize)
	build := func(tb testing.TB, payload []byte, harnessSigns bool) *mempool.Tx {
		var a StreamAction
		decoded := json.Unmarshal(payload, &a) == nil
		if harnessSigns && decoded {
			key := payer
			if a.Action == StreamActionReceipt || a.Action == StreamActionSettle {
				key = provider
			}
			return sign(tb, key, a)
		}
		return &mempool.Tx{ID: a.ID, Sender: a.Sender, Nonce: a.Nonce, Payload: payload,
			ContractID: StreamContractID, PublicKey: hex.EncodeToString(payer.pub), Signature: zeroSignature}
	}

	// The state both runs start from: the payer funded, and stream
	// vpn-device-1 opened at height 1 with the rule off.
	established := sign(f, payer, open("stream-open-1", "vpn-device-1", 0))
	newApplier := func(t *testing.T, height uint64, withHeight bool) *EnrollmentAwareApplier {
		accounts := NewAccountStore()
		accounts.Credit(payer.address, 10)
		aware := NewEnrollmentAwareApplier(accounts, nil)
		aware.SetStreamStateStore(NewStreamStateStore())
		aware.SetHeightFn(func() uint64 { return 1 })
		if err := aware.ApplyTx(cloneTx(established)); err != nil {
			t.Fatalf("established open: %v", err)
		}
		aware.SetHeightFn(nil)
		if withHeight {
			aware.SetHeightFn(func() uint64 { return height })
		}
		return aware
	}
	newReplay := func(t *testing.T) *StreamStateStore {
		s := NewStreamStateStore()
		if err := s.ApplyHistoricalTx(cloneTx(established), 1); err != nil {
			t.Fatalf("established open: %v", err)
		}
		return s
	}

	receipt := signedUsageReceipt(f, sessionPrivate, StreamUsageReceipt{
		StreamID: "vpn-device-1", Sequence: 1, CumulativeActiveSeconds: 60, ObservedAt: "2026-08-01T00:01:00Z",
	})
	encode := func(a StreamAction) []byte {
		b, err := json.Marshal(a)
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	second := encode(open("stream-open-2", "vpn-device-2", 1))
	lifecycle := func(id, action string, nonce uint64, at string) []byte {
		return encode(StreamAction{ID: id, StreamID: "vpn-device-1", Action: action, Nonce: nonce, Timestamp: at})
	}
	for _, s := range []struct {
		payload            []byte
		signs              bool
		height, retirement uint64
		withHeight         bool
	}{
		{second, true, 99, 100, true},
		{second, true, 100, 100, true},
		{second, true, 1 << 40, 0, true},
		{second, true, 5, 100, false},
		{second, false, 100, 100, true},
		{encode(StreamAction{ID: "stream-receipt-1", StreamID: "vpn-device-1", Action: StreamActionReceipt,
			Receipt: &receipt, Timestamp: "2026-08-01T00:01:01Z"}), true, 150, 100, true},
		{lifecycle("stream-pause", StreamActionPause, 1, "2026-08-01T00:02:00Z"), true, 150, 100, false},
		{lifecycle("stream-settle", StreamActionSettle, 0, "2026-08-01T00:04:00Z"), true, 100, 100, true},
		{lifecycle("stream-close", StreamActionClose, 1, "2026-08-01T00:05:00Z"), true, 150, 100, true},
		{bytes.Replace(second, []byte(`"open"`), []byte(`"OPEN"`), 1), true, 150, 100, true},
		{[]byte(`{"action":"open"}`), true, 150, 100, true},
		{[]byte(`{}`), false, 0, 1, true},
		{[]byte(`null`), true, 0, 0, false},
	} {
		f.Add(s.payload, s.signs, s.height, s.retirement, s.withHeight)
	}

	f.Fuzz(func(t *testing.T, payload []byte, harnessSigns bool, height, retirement uint64, withHeight bool) {
		tx := build(t, payload, harnessSigns)
		prev := StreamEd25519RetirementHeight()
		defer SetStreamEd25519RetirementHeight(prev)
		action, decodeErr := DecodeStreamActionTx(tx)
		isOpen := decodeErr == nil && action.Action == StreamActionOpen

		// Block applier.
		SetStreamEd25519RetirementHeight(0)
		off, on := newApplier(t, height, withHeight), newApplier(t, height, withHeight)
		errOff := off.ApplyTx(cloneTx(tx))
		rootBefore := on.StateRoot()
		SetStreamEd25519RetirementHeight(retirement)
		errOn := on.ApplyTx(cloneTx(tx))
		switch {
		case retirement != 0 && isOpen && !withHeight:
			if !errors.Is(errOn, ErrEnrollmentHeightUnset) || on.StateRoot() != rootBefore {
				t.Fatalf("open with H=%d and no height source: %v (state changed: %v)", retirement, errOn, on.StateRoot() != rootBefore)
			}
		case isOpen && StreamEd25519SessionKeysRetiredAt(height):
			if !errors.Is(errOn, ErrStreamEd25519SessionKeyRetired) || on.StateRoot() != rootBefore {
				t.Fatalf("open at height %d with H=%d: %v (state changed: %v)", height, retirement, errOn, on.StateRoot() != rootBefore)
			}
		default:
			if fmt.Sprint(errOn) != fmt.Sprint(errOff) || on.StateRoot() != off.StateRoot() {
				t.Fatalf("the rule changed a transaction it does not govern (height %d, H=%d, open %v): %v vs %v",
					height, retirement, isOpen, errOn, errOff)
			}
		}

		// Boot replay of the stream projection. Replay verifies before it
		// applies the rule, so only a verified open is refused as retired.
		SetStreamEd25519RetirementHeight(0)
		replayOff, replayOn := newReplay(t), newReplay(t)
		errOff = replayOff.ApplyHistoricalTx(cloneTx(tx), height)
		rootBefore = replayOn.StateRoot()
		SetStreamEd25519RetirementHeight(retirement)
		errOn = replayOn.ApplyHistoricalTx(cloneTx(tx), height)
		if isOpen && VerifyStreamActionTx(tx) == nil && StreamEd25519SessionKeysRetiredAt(height) {
			if !errors.Is(errOn, ErrStreamEd25519SessionKeyRetired) || replayOn.StateRoot() != rootBefore {
				t.Fatalf("replayed open at height %d with H=%d: %v (state changed: %v)", height, retirement, errOn, replayOn.StateRoot() != rootBefore)
			}
		} else if fmt.Sprint(errOn) != fmt.Sprint(errOff) || replayOn.StateRoot() != replayOff.StateRoot() {
			t.Fatalf("the rule changed a replayed transaction it does not govern (height %d, H=%d, open %v): %v vs %v",
				height, retirement, isOpen, errOn, errOff)
		}
	})
}
