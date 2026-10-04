package cc

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

// atomicRaceWorkers is the HL1 WP2 acceptance fan-out: this many concurrent
// submissions sharing one nonce must produce exactly one acceptance.
const atomicRaceWorkers = 200

// Every verification reaches the nonce store before any may complete it, so
// all workers race on the same nonce. Seen shares the barrier: a verifier that
// still used the non-atomic Seen+Record pair would let every worker observe
// the nonce as unseen and accept, which the single-winner count rejects.
// Record is inherited unchanged from the embedded store.
type atomicNonceBarrierStore struct {
	*hmac.InMemoryNonceStore
	arrived chan struct{}
	release chan struct{}
}

func (s *atomicNonceBarrierStore) wait() {
	s.arrived <- struct{}{}
	<-s.release
}

func (s *atomicNonceBarrierStore) TryRecord(node string, nonce [32]byte, at time.Time) bool {
	s.wait()
	return s.InMemoryNonceStore.TryRecord(node, nonce, at)
}

// Seen waits after its lookup so every worker observes the pre-claim state.
func (s *atomicNonceBarrierStore) Seen(node string, nonce [32]byte) bool {
	seen := s.InMemoryNonceStore.Seen(node, nonce)
	s.wait()
	return seen
}

func TestVerifierAtomicNonceConcurrentSingleAccept(t *testing.T) {
	_, proof, _, v, now := buildHappyPath(t, BuildOpts{})
	bundle, err := ParseBundle(proof.Attestation.BundleBase64)
	if err != nil {
		t.Fatal(err)
	}
	store := &atomicNonceBarrierStore{
		InMemoryNonceStore: hmac.NewInMemoryNonceStore(2 * mining.FreshnessWindow),
		arrived:            make(chan struct{}, atomicRaceWorkers),
		release:            make(chan struct{}),
	}
	v.nonceStore = store
	results := make(chan error, atomicRaceWorkers)
	var release sync.Once
	defer release.Do(func() { close(store.release) })
	for i := 0; i < atomicRaceWorkers; i++ {
		go func() { results <- v.VerifyAttestation(proof, now) }()
	}
	timeout := time.NewTimer(60 * time.Second)
	defer timeout.Stop()
	for i := 0; i < atomicRaceWorkers; i++ {
		select {
		case <-store.arrived:
		case err := <-results:
			t.Fatalf("verification returned before reaching the nonce store: %v", err)
		case <-timeout.C:
			t.Fatal("verifications did not all reach the nonce-store barrier")
		}
	}
	release.Do(func() { close(store.release) })
	winners, replays := 0, 0
	for i := 0; i < atomicRaceWorkers; i++ {
		select {
		case err := <-results:
			switch {
			case err == nil:
				winners++
			case errors.Is(err, mining.ErrAttestationNonceMismatch):
				replays++
			default:
				t.Fatalf("unexpected rejection: %v", err)
			}
		case <-timeout.C:
			t.Fatal("concurrent attestation verification did not complete")
		}
	}
	if winners != 1 || replays != atomicRaceWorkers-1 {
		t.Fatalf("accepted=%d replays=%d, want 1 and %d", winners, replays, atomicRaceWorkers-1)
	}
	if !store.InMemoryNonceStore.Seen(bundle.DeviceUUID, proof.Attestation.Nonce) {
		t.Fatal("accepted attestation did not leave its nonce claimed")
	}
}
