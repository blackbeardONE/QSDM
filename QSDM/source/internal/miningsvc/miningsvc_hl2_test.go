package miningsvc

// HL2 WP-B: Submit with a version 2 legacy-mining config uses the guard's
// per-owner surface (legacymining.OwnerGuard). A v1 config keeps the HL1
// calls (miningsvc_hl1_test.go, unchanged).

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/api"
)

// fakeOwnerGuard is a fakeGuard with a v2 config and the OwnerGuard methods.
type fakeOwnerGuard struct {
	*fakeGuard
	ownerRateErr error
	pendingErr   error

	omu      sync.Mutex
	owners   []string // CheckOwnerPending arguments
	observed []legacymining.Candidate
}

func newFakeOwnerGuard(log *callLog) *fakeOwnerGuard {
	g := &fakeOwnerGuard{fakeGuard: newFakeGuard(log)}
	g.cfg.Version = legacymining.ConfigVersion2
	return g
}

// Precheck attributes the candidate to its miner_addr, as the real v2
// Precheck does after the enrollment lookup.
func (g *fakeOwnerGuard) Precheck(raw []byte) (legacymining.Candidate, error) {
	c, err := g.fakeGuard.Precheck(raw)
	if err == nil && c.Proof != nil {
		c.Owner = c.Proof.MinerAddr
	}
	return c, err
}

func (g *fakeOwnerGuard) TakeOwnerRate(legacymining.Candidate) error {
	g.log.add("TakeOwnerRate")
	return g.ownerRateErr
}

func (g *fakeOwnerGuard) CheckOwnerPending(owner string) error {
	g.log.add("CheckOwnerPending")
	g.omu.Lock()
	g.owners = append(g.owners, owner)
	g.omu.Unlock()
	return g.pendingErr
}

func (g *fakeOwnerGuard) ObserveOwnerRejection(c legacymining.Candidate, err error) {
	g.log.add("ObserveOwnerRejection")
	g.omu.Lock()
	g.observed = append(g.observed, c)
	g.omu.Unlock()
}

func (g *fakeOwnerGuard) SetOwnerOutstanding(string, int)      {}
func (g *fakeOwnerGuard) ResetOwnerOutstanding(map[string]int) {}

var _ legacymining.OwnerGuard = (*fakeOwnerGuard)(nil)

func TestHL2NewV2ConfigRequiresOwnerGuard(t *testing.T) {
	withoutPinnedCompat(t)
	f := newHL1Fixture(t, 1, 0)
	f.guard.cfg.Version = legacymining.ConfigVersion2
	if svc, err := New(f.cfg); err == nil || svc != nil || !strings.Contains(err.Error(), "OwnerGuard") {
		t.Fatalf("New(v2, plain Guard) = %v, %v; want refusal", svc, err)
	}
	// A read-only service has no guard and is not affected.
	f.cfg.ReadOnly = true
	if _, err := New(f.cfg); err != nil {
		t.Fatalf("read-only: %v", err)
	}
}

func hl2Service(t *testing.T) (*hl1Fixture, *fakeOwnerGuard, *Service) {
	t.Helper()
	f := newHL1Fixture(t, 2, 0)
	g := newFakeOwnerGuard(f.log)
	f.guard = g.fakeGuard
	f.cfg.Guard = g
	return f, g, f.service(t)
}

func TestHL2SubmitUsesOwnerSurface(t *testing.T) {
	f, g, svc := hl2Service(t)
	tip := svc.TipHeight()
	solver := newProofSolver(t, svc)
	raw := solver.solve(t, tip, headerAt(t, f.bp, tip), "qsdm1owner")
	f.log.reset()
	f.log.trackSubmitMu(svc)
	if _, err := svc.Submit(raw); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	want := []call{
		{"Admit", false}, {"Precheck", false}, {"TakeOwnerRate", false},
		{"Outstanding", true}, {"CheckOwnerPending", true}, {"GetBlock", true}, {"Accept", true}, {"Enqueue", true},
	}
	var got []call
	for _, c := range f.log.snapshot() {
		if len(got) == 0 || got[len(got)-1] != c {
			got = append(got, c)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call order\n got %v\nwant %v", got, want)
	}
	if !reflect.DeepEqual(g.owners, []string{"qsdm1owner"}) {
		t.Fatalf("CheckOwnerPending owners %v", g.owners)
	}

	// A step 7 rejection goes to ObserveOwnerRejection with the candidate,
	// never to the unattributed ObserveRejection.
	f.log.reset()
	if _, err := svc.Submit(raw); err == nil {
		t.Fatal("duplicate accepted")
	}
	names := f.log.names()
	if names[len(names)-1] != "ObserveOwnerRejection" || len(g.observedErrs()) != 0 {
		t.Fatalf("calls %v, unattributed observed %v", names, g.observedErrs())
	}
	if len(g.observed) != 1 || g.observed[0].Owner != "qsdm1owner" {
		t.Fatalf("observed candidates %+v", g.observed)
	}
}

func TestHL2SubmitOwnerRejectionsAre503(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   func(*fakeOwnerGuard)
		kind  legacymining.RejectKind
		calls []string
	}{
		{"owner cooldown", func(g *fakeOwnerGuard) {
			g.ownerRateErr = &legacymining.Rejection{Kind: legacymining.KindOwnerCooldown}
		}, legacymining.KindOwnerCooldown, []string{"Admit", "Precheck", "TakeOwnerRate"}},
		{"owner bucket", func(g *fakeOwnerGuard) {
			g.ownerRateErr = &legacymining.Rejection{Kind: legacymining.KindOwnerRateLimited}
		}, legacymining.KindOwnerRateLimited, []string{"Admit", "Precheck", "TakeOwnerRate"}},
		{"owner pending", func(g *fakeOwnerGuard) {
			g.pendingErr = &legacymining.Rejection{Kind: legacymining.KindOwnerPendingFull}
		}, legacymining.KindOwnerPendingFull, []string{"Admit", "Precheck", "TakeOwnerRate", "Outstanding", "CheckOwnerPending"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, g, svc := hl2Service(t)
			tip := svc.TipHeight()
			raw := newProofSolver(t, svc).solve(t, tip, headerAt(t, f.bp, tip), "qsdm1owner")
			tc.set(g)
			f.log.reset()
			_, err := svc.Submit(raw)
			if !errors.Is(err, api.ErrMiningUnavailable) || legacymining.RejectKindOf(err) != tc.kind || tc.kind.HTTPStatus() != http.StatusServiceUnavailable {
				t.Fatalf("Submit = %v, want 503 %v", err, tc.kind)
			}
			if got := f.log.names(); !reflect.DeepEqual(got, tc.calls) {
				t.Fatalf("calls %v, want %v", got, tc.calls)
			}
			if f.store.count() != 0 {
				t.Fatal("rejected proof stored")
			}
		})
	}
}
