package networking

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestBFTGossipRejectedPublisherCannotPoisonDedupe(t *testing.T) {
	for _, kind := range []string{chain.BFTWirePropose, chain.BFTWirePrevote, chain.BFTWirePrecommit} {
		for _, missingPublisher := range []bool{false, true} {
			name := kind + "/unauthorized"
			if missingPublisher {
				name = kind + "/missing"
			}
			t.Run(name, func(t *testing.T) {
				policy, signer, address, publisher, root := newBFTGossipPeerOriginFixture(t)
				ingress := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
				ingress.SetPeerOriginPolicy(policy)
				payload := signedPublisherDedupePayload(t, kind, signer, address, root)

				var err error
				wantErr := chain.ErrBFTPeerOriginUnauthorized
				if missingPublisher {
					wantErr = chain.ErrBFTPeerOriginMissing
					err = ingress.HandlePeerMessage("untrusted-relay", payload)
				} else {
					err = ingress.HandlePeerMessageFromPublisher("untrusted-relay", testIngressPeerID(t), payload)
				}
				if !errors.Is(err, wantErr) {
					t.Fatalf("untrusted copy error = %v, want %v", err, wantErr)
				}
				if err := ingress.HandlePeerMessageFromPublisher("honest-relay", publisher, payload); err != nil {
					t.Fatalf("authorized delivery after rejected copy must succeed: %v", err)
				}
				if err := ingress.HandlePeerMessageFromPublisher("other-relay", publisher, payload); err == nil || !strings.Contains(err.Error(), "duplicate bft gossip") {
					t.Fatalf("accepted payload must still be deduplicated: %v", err)
				}
				if stats := ingress.Stats(); stats.PublisherRejected != 1 || stats.IngressOK != 1 || stats.DedupeDropped != 1 {
					t.Fatalf("unexpected delivery stats: %+v", stats)
				}
			})
		}
	}
}

func TestBFTGossipPublisherValidationKeepsPreAuthRateLimit(t *testing.T) {
	policy, signer, address, publisher, root := newBFTGossipPeerOriginFixture(t)
	cfg := DefaultBFTGossipConfig()
	cfg.MaxPerWindow = 1
	ingress := NewBFTGossipIngress(cfg, nil)
	ingress.SetPeerOriginPolicy(policy)
	payload := signedPublisherDedupePayload(t, chain.BFTWirePrevote, signer, address, root)
	if err := ingress.HandlePeerMessage("limited-relay", payload); !errors.Is(err, chain.ErrBFTPeerOriginMissing) {
		t.Fatalf("first attempt error = %v, want missing publisher", err)
	}
	if err := ingress.HandlePeerMessage("limited-relay", payload); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("rejected publisher must still consume rate limit: %v", err)
	}
	if err := ingress.HandlePeerMessageFromPublisher("honest-relay", publisher, payload); err != nil {
		t.Fatalf("another relay's authorized delivery must succeed: %v", err)
	}
	if stats := ingress.Stats(); stats.PublisherRejected != 1 || stats.RateLimited != 1 || stats.IngressOK != 1 || stats.DedupeDropped != 0 {
		t.Fatalf("unexpected rate-limit stats: %+v", stats)
	}
}

func TestBFTGossipConcurrentAuthorizedPublisherDedupes(t *testing.T) {
	policy, signer, address, publisher, root := newBFTGossipPeerOriginFixture(t)
	ingress := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	ingress.SetPeerOriginPolicy(policy)
	payload := signedPublisherDedupePayload(t, chain.BFTWirePrevote, signer, address, root)
	const deliveries = 16
	start := make(chan struct{})
	results := make(chan error, deliveries)
	var wg sync.WaitGroup
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- ingress.HandlePeerMessageFromPublisher("honest-relay", publisher, payload)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !strings.Contains(err.Error(), "duplicate bft gossip") {
			t.Fatalf("unexpected concurrent delivery error: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent copies, want 1", accepted)
	}
	if stats := ingress.Stats(); stats.IngressOK != 1 || stats.DedupeDropped != deliveries-1 || stats.PublisherRejected != 0 {
		t.Fatalf("unexpected concurrent delivery stats: %+v", stats)
	}
}

func signedPublisherDedupePayload(t *testing.T, kind string, signer chain.BFTSigner, address, root string) []byte {
	t.Helper()
	var message any
	switch kind {
	case chain.BFTWirePropose:
		m := chain.BFTWireProposeMsg{Height: 10, Round: 1, Proposer: address, BlockHash: "root", MembershipRoot: root}
		if err := chain.SignPropose(&m, signer); err != nil {
			t.Fatal(err)
		}
		message = m
	case chain.BFTWirePrevote:
		m := chain.BFTWirePrevoteMsg{Height: 10, Round: 1, Validator: address, BlockHash: "root", MembershipRoot: root}
		if err := chain.SignPrevote(&m, signer); err != nil {
			t.Fatal(err)
		}
		message = m
	case chain.BFTWirePrecommit:
		m := chain.BFTWirePrecommitMsg{Height: 10, Round: 1, Validator: address, BlockHash: "root", MembershipRoot: root}
		if err := chain.SignPrecommit(&m, signer); err != nil {
			t.Fatal(err)
		}
		message = m
	default:
		t.Fatalf("unsupported message kind %s", kind)
	}
	payload, err := chain.MarshalBFTWire(kind, message)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
