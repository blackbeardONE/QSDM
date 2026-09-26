package miningsvc

// HL1 admission surface (design rev 4 §2 miningsvc notes, §4.1, §6.2).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// ---- New --------------------------------------------------------------------

func TestHL1NewWritableRequiresStoreGuardAndSink(t *testing.T) {
	withoutPinnedCompat(t)
	for _, tc := range []struct {
		name  string
		edit  func(*Config)
		field string
	}{
		{"none", func(c *Config) { c.Store, c.Guard, c.Sink = nil, nil, nil }, "Store"},
		{"no store", func(c *Config) { c.Store = nil }, "Store"},
		{"no guard", func(c *Config) { c.Guard = nil }, "Guard"},
		{"no sink", func(c *Config) { c.Sink = nil }, "Sink"},
		{"typed nil store", func(c *Config) { c.Store = (*fakeStore)(nil) }, "Store"},
		{"typed nil guard", func(c *Config) { c.Guard = (*fakeGuard)(nil) }, "Guard"},
		{"typed nil sink", func(c *Config) { c.Sink = (*fakeSink)(nil) }, "Sink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHL1Fixture(t, 1, 0)
			tc.edit(&f.cfg)
			svc, err := New(f.cfg)
			if err == nil || svc != nil {
				t.Fatalf("New = %v, %v; want refusal", svc, err)
			}
			if !strings.Contains(err.Error(), "Config."+tc.field) {
				t.Fatalf("error %q does not name Config.%s", err, tc.field)
			}
		})
	}
	f := newHL1Fixture(t, 1, 0)
	if _, err := New(f.cfg); err != nil {
		t.Fatalf("complete writable Config refused: %v", err)
	}
}

func TestHL1NewRefusesRewardSink(t *testing.T) {
	for _, tc := range []struct {
		name     string
		compat   bool
		readOnly bool
		collabs  bool
	}{
		{"writable with collaborators", false, false, true},
		{"writable with collaborators, compat hook present", true, false, true},
		{"writable without collaborators", false, false, false},
		{"read-only", false, true, false},
		{"read-only, compat hook present", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.compat {
				withoutPinnedCompat(t)
			}
			f := newHL1Fixture(t, 1, 0)
			f.cfg.ReadOnly = tc.readOnly
			if !tc.collabs {
				f.cfg.Store, f.cfg.Guard, f.cfg.Sink = nil, nil, nil
			}
			f.cfg.RewardSink = &capturingSink{}
			if svc, err := New(f.cfg); err == nil || svc != nil || !strings.Contains(err.Error(), "RewardSink") {
				t.Fatalf("New = %v, %v; want RewardSink refusal", svc, err)
			}
		})
	}
}

func TestHL1NewReadOnlyNeedsNoCollaborators(t *testing.T) {
	withoutPinnedCompat(t)
	f := newHL1Fixture(t, 1, 0)
	f.cfg.ReadOnly = true
	f.cfg.Store, f.cfg.Guard, f.cfg.Sink = nil, nil, nil
	svc, err := New(f.cfg)
	if err != nil {
		t.Fatalf("read-only New: %v", err)
	}
	if _, err := svc.WorkAt(0); !errors.Is(err, api.ErrMiningUnavailable) {
		t.Fatalf("read-only WorkAt = %v, want ErrMiningUnavailable", err)
	}
	if _, err := svc.Submit([]byte(`{}`)); !errors.Is(err, api.ErrMiningUnavailable) {
		t.Fatalf("read-only Submit = %v, want ErrMiningUnavailable", err)
	}
}

func TestHL1NewRefusesTypedNilProducer(t *testing.T) {
	f := newHL1Fixture(t, 1, 0)
	f.cfg.Producer = (*chain.BlockProducer)(nil)
	if svc, err := New(f.cfg); err == nil || svc != nil {
		t.Fatalf("New with typed nil producer = %v, %v", svc, err)
	}
}

// The hook that keeps the pinned tests running must never be set by
// production code.
func TestHL1PinnedCompatHookIsTestOnly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == "pinnedTestCollaborators" {
						t.Errorf("%s: production code assigns pinnedTestCollaborators", fset.Position(n.Pos()))
					}
				}
			case *ast.ValueSpec:
				for _, id := range n.Names {
					if id.Name == "pinnedTestCollaborators" && len(n.Values) != 0 {
						t.Errorf("%s: pinnedTestCollaborators has an initializer", fset.Position(n.Pos()))
					}
				}
			case *ast.UnaryExpr:
				if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND && id.Name == "pinnedTestCollaborators" {
					t.Errorf("%s: production code takes the address of pinnedTestCollaborators", fset.Position(n.Pos()))
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no production files checked")
	}
}

// ---- WorkAt and the durable view ------------------------------------------

func TestHL1WorkAtRequiresAdmissionOpen(t *testing.T) {
	f := newHL1Fixture(t, 1, 0)
	svc := f.service(t)
	api.SetMiningService(svc)
	t.Cleanup(func() { api.SetMiningService(nil) })
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		(&api.Handlers{}).MiningWorkHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/mining/work", nil))
		return rec
	}
	for _, open := range []bool{false, true, false} {
		f.guard.setClosed(!open)
		_, err := svc.WorkAt(svc.TipHeight())
		rec := get()
		if open {
			if err != nil || rec.Code != http.StatusOK {
				t.Fatalf("admission open: WorkAt err=%v, HTTP %d", err, rec.Code)
			}
			continue
		}
		if !errors.Is(err, api.ErrMiningUnavailable) {
			t.Fatalf("admission closed: WorkAt err=%v, want ErrMiningUnavailable", err)
		}
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
			t.Fatalf("admission closed: HTTP %d Retry-After=%q, want 503 and 5", rec.Code, rec.Header().Get("Retry-After"))
		}
	}
}

func TestHL1DurableTipLimitsWorkVerificationAndAcceptHeight(t *testing.T) {
	f := newHL1Fixture(t, 3, 1)
	svc := f.service(t)
	producerTip := f.bp.TipHeight()
	durable := producerTip - 1
	if got := svc.TipHeight(); got != durable {
		t.Fatalf("TipHeight = %d, want durable tip %d (producer %d)", got, durable, producerTip)
	}
	for _, h := range []uint64{producerTip + 1, producerTip} {
		work, err := svc.WorkAt(h)
		if err != nil {
			t.Fatalf("WorkAt(%d): %v", h, err)
		}
		if work.Height != durable {
			t.Fatalf("WorkAt(%d).Height = %d, want durable tip %d", h, work.Height, durable)
		}
	}
	if _, ok := (chainAdapter{producer: f.view}).HeaderHashAt(producerTip); ok {
		t.Fatal("HeaderHashAt served a block above the durable tip")
	}
	// Defence in depth: the adapter refuses heights above the view's tip even
	// if the view's GetBlock is not clamped.
	if _, ok := (chainAdapter{producer: unclampedView{f.view}}).HeaderHashAt(producerTip); ok {
		t.Fatal("chainAdapter used a block above the view's tip")
	}

	ps := newProofSolver(t, svc)
	// A proof on the non-durable block is not verifiable yet.
	above := ps.solve(t, producerTip, headerAt(t, f.bp, producerTip), "qsdm1above")
	_, err := svc.Submit(above)
	var rej *mining.RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("proof above the durable tip: err=%v, want a 400 RejectError", err)
	}
	if f.store.count() != 0 || len(f.guard.observedErrs()) != 1 {
		t.Fatalf("proof above the durable tip: rows=%d observed=%d", f.store.count(), len(f.guard.observedErrs()))
	}
	// A proof on the durable tip is accepted at the durable accept height.
	raw := ps.solve(t, durable, headerAt(t, f.bp, durable), "qsdm1durable")
	id, err := svc.Submit(raw)
	if err != nil {
		t.Fatalf("Submit at durable tip: %v", err)
	}
	row, ok := f.store.row(id)
	if !ok || row.AcceptTip != durable || row.WorkHeight != durable {
		t.Fatalf("row=%+v ok=%v; want AcceptTip=WorkHeight=%d (producer tip %d)", row, ok, durable, producerTip)
	}

	// No durable tip yet (before S5): no work and no admission.
	f.view.has.Store(false)
	if _, err := svc.WorkAt(0); !errors.Is(err, api.ErrMiningUnavailable) {
		t.Fatalf("WorkAt without durable tip = %v", err)
	}
	before := f.store.count()
	if _, err := svc.Submit(ps.solve(t, durable, headerAt(t, f.bp, durable), "qsdm1notip")); !errors.Is(err, api.ErrMiningUnavailable) {
		t.Fatalf("Submit without durable tip = %v, want ErrMiningUnavailable", err)
	}
	if f.store.count() != before {
		t.Fatal("Submit without durable tip reached the store")
	}
}

// unclampedView exposes every block of the inner producer through GetBlock,
// while HasTip and TipHeight still report the durable view.
type unclampedView struct{ v *durableView }

func (u unclampedView) HasTip() bool      { return u.v.HasTip() }
func (u unclampedView) TipHeight() uint64 { return u.v.TipHeight() }
func (u unclampedView) GetBlock(h uint64) (*chain.Block, bool) {
	return u.v.inner.GetBlock(h)
}

// ---- Submit: §4.1 order ---------------------------------------------------

func TestHL1SubmitOrderAndLocking(t *testing.T) {
	f := newHL1Fixture(t, 2, 0)
	svc := f.service(t)
	tip := svc.TipHeight()
	raw := newProofSolver(t, svc).solve(t, tip, headerAt(t, f.bp, tip), "qsdm1order")
	f.log.reset()
	f.log.trackSubmitMu(svc)

	before := time.Now().UnixNano()
	id, err := svc.Submit(raw)
	after := time.Now().UnixNano()
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	want := []call{
		{"Admit", false}, {"Precheck", false}, {"TakeRate", false},
		{"Outstanding", true}, {"GetBlock", true}, {"Accept", true}, {"Enqueue", true},
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

	row, ok := f.store.row(id)
	if !ok {
		t.Fatal("accepted proof not in store")
	}
	p, _ := mining.ParseProof(raw)
	wantRow := legacymining.Record{
		ProofID:      id,
		MinerAddr:    "qsdm1order",
		NodeID:       "node-A",
		AttNonce:     row.AttNonce,
		WorkHeight:   p.Height,
		AcceptTip:    tip,
		AcceptedNS:   row.AcceptedNS,
		ConfigSHA256: f.guard.hash,
		ProofJSON:    raw,
	}
	if !reflect.DeepEqual(row, wantRow) {
		t.Fatalf("row\n got %+v\nwant %+v", row, wantRow)
	}
	if row.AcceptedNS < before || row.AcceptedNS > after {
		t.Fatalf("AcceptedNS %d outside [%d, %d]", row.AcceptedNS, before, after)
	}
	if row.AttNonce == ([32]byte{}) {
		t.Fatal("AttNonce not taken from the candidate")
	}
	if enq := f.sink.records(); len(enq) != 1 || !reflect.DeepEqual(enq[0], row) {
		t.Fatalf("enqueued %+v, want exactly the committed row", enq)
	}
	if len(f.guard.observedErrs()) != 0 || len(f.guard.freezeCauses()) != 0 {
		t.Fatalf("success path observed=%v freezes=%v", f.guard.observedErrs(), f.guard.freezeCauses())
	}
	// The stored proof bytes are a copy.
	orig := append([]byte(nil), raw...)
	raw[0] ^= 0xff
	if row2, _ := f.store.row(id); !bytes.Equal(row2.ProofJSON, orig) {
		t.Fatal("stored ProofJSON aliases the request buffer")
	}
}

type submitCase struct {
	name      string
	setup     func(t *testing.T, f *hl1Fixture, svc *Service, raw []byte) []byte
	wantCalls []string
	status    int                     // 400 or 503
	kind      legacymining.RejectKind // 0: not a *Rejection
	reason    mining.RejectReason     // for 400
	observed  int
	freeze    string // expected freeze cause prefix, "" for none
	rows      int
}

func TestHL1SubmitStepOutcomes(t *testing.T) {
	sentinel := errors.New("disk I/O error")
	for _, tc := range []submitCase{
		{
			name: "step 3 admission closed",
			setup: func(_ *testing.T, f *hl1Fixture, _ *Service, raw []byte) []byte {
				f.guard.setClosed(true)
				return raw
			},
			wantCalls: []string{"Admit"},
			status:    503, kind: legacymining.KindAdmissionClosed,
		},
		{
			name: "step 4 precheck rejection is 400 and not observed",
			setup: func(_ *testing.T, f *hl1Fixture, _ *Service, raw []byte) []byte {
				f.guard.precheckErr = &legacymining.Rejection{Kind: legacymining.KindMinerNotAllowed}
				return raw
			},
			wantCalls: []string{"Admit", "Precheck"},
			status:    400, kind: legacymining.KindMinerNotAllowed, reason: mining.ReasonBadAddr,
		},
		{
			name: "step 4 malformed proof",
			setup: func(_ *testing.T, _ *hl1Fixture, _ *Service, _ []byte) []byte {
				return []byte(`{"not":"a proof"`)
			},
			wantCalls: []string{"Admit", "Precheck"},
			status:    400, kind: legacymining.KindMalformed, reason: mining.ReasonNonCanonical,
		},
		{
			name: "step 4 precheck without proof fails closed",
			setup: func(_ *testing.T, f *hl1Fixture, _ *Service, raw []byte) []byte {
				f.guard.nilProof = true
				return raw
			},
			wantCalls: []string{"Admit", "Precheck"},
			status:    503,
		},
		{
			name: "step 5 per-minute cap",
			setup: func(_ *testing.T, f *hl1Fixture, _ *Service, raw []byte) []byte {
				f.guard.rateErr = &legacymining.Rejection{Kind: legacymining.KindRateLimited}
				return raw
			},
			wantCalls: []string{"Admit", "Precheck", "TakeRate"},
			status:    503, kind: legacymining.KindRateLimited,
		},
		{
			name: "step 6 pending plus in-flight at max_pending",
			setup: func(_ *testing.T, f *hl1Fixture, _ *Service, raw []byte) []byte {
				f.guard.withMaxPending(3)
				f.sink.setOutstanding(3)
				return raw
			},
			wantCalls: []string{"Admit", "Precheck", "TakeRate", "Outstanding"},
			status:    503, kind: legacymining.KindPendingFull,
		},
		{
			name: "step 7 verifier rejection is observed",
			setup: func(t *testing.T, f *hl1Fixture, svc *Service, _ []byte) []byte {
				var wrong [32]byte
				wrong[0] = 0xff
				return newProofSolver(t, svc).solve(t, svc.TipHeight(), wrong, "qsdm1tampered")
			},
			wantCalls: []string{"Admit", "Precheck", "TakeRate", "Outstanding", "GetBlock", "ObserveRejection"},
			status:    400, reason: mining.ReasonHeaderMismatch, observed: 1,
		},
		{
			name: "step 8 duplicate after restart is 400, no freeze",
			setup: func(t *testing.T, f *hl1Fixture, svc *Service, raw []byte) []byte {
				// A first service accepts the proof; the fixture's service
				// has a fresh in-memory ProofIDSet but the same store.
				first := f.service(t)
				if _, err := first.Submit(raw); err != nil {
					t.Fatalf("first Submit: %v", err)
				}
				f.sink.setOutstanding(0)
				return raw
			},
			wantCalls: []string{"Admit", "Precheck", "TakeRate", "Outstanding", "GetBlock", "Accept", "ObserveRejection"},
			status:    400, kind: legacymining.KindDuplicate, reason: mining.ReasonDuplicate, observed: 1, rows: 1,
		},
		{
			name: "step 8 nonce conflict is 400, no freeze",
			setup: func(t *testing.T, f *hl1Fixture, svc *Service, raw []byte) []byte {
				f.guard.withNonce([32]byte{7})
				other := newProofSolver(t, svc).solve(t, svc.TipHeight(), headerAt(t, f.bp, svc.TipHeight()), "qsdm1other")
				if _, err := svc.Submit(other); err != nil {
					t.Fatalf("first Submit: %v", err)
				}
				return raw
			},
			wantCalls: []string{"Admit", "Precheck", "TakeRate", "Outstanding", "GetBlock", "Accept", "ObserveRejection"},
			status:    400, kind: legacymining.KindNonceConflict, reason: mining.ReasonAttestation, observed: 1, rows: 1,
		},
		{
			name: "step 8 store I/O error freezes",
			setup: func(_ *testing.T, f *hl1Fixture, _ *Service, raw []byte) []byte {
				f.store.acceptErr = sentinel
				return raw
			},
			wantCalls: []string{"Admit", "Precheck", "TakeRate", "Outstanding", "GetBlock", "Accept", "ObserveRejection", "Freeze"},
			status:    503, kind: legacymining.KindUnavailable, observed: 1, freeze: legacymining.CauseAcceptIO + ":",
		},
		{
			name: "step 8 unexpected rejection kind freezes",
			setup: func(_ *testing.T, f *hl1Fixture, _ *Service, raw []byte) []byte {
				f.store.acceptErr = &legacymining.Rejection{Kind: legacymining.KindMalformed}
				return raw
			},
			wantCalls: []string{"Admit", "Precheck", "TakeRate", "Outstanding", "GetBlock", "Accept", "ObserveRejection", "Freeze"},
			status:    503, kind: legacymining.KindUnavailable, observed: 1, freeze: legacymining.CauseAcceptIO + ":",
		},
		{
			name: "step 9 enqueue failure freezes, row stays committed",
			setup: func(_ *testing.T, f *hl1Fixture, _ *Service, raw []byte) []byte {
				f.sink.enqueueErr = legacymining.ErrNotInitialized
				return raw
			},
			wantCalls: []string{"Admit", "Precheck", "TakeRate", "Outstanding", "GetBlock", "Accept", "Enqueue", "Freeze"},
			status:    503, kind: legacymining.KindUnavailable, freeze: legacymining.CauseEnqueue + ":", rows: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHL1Fixture(t, 1, 0)
			svc := f.service(t)
			tip := svc.TipHeight()
			raw := newProofSolver(t, svc).solve(t, tip, headerAt(t, f.bp, tip), "qsdm1case")
			raw = tc.setup(t, f, svc, raw)
			f.log.reset()
			observedBefore := len(f.guard.observedErrs())

			id, err := svc.Submit(raw)
			if err == nil {
				t.Fatalf("Submit accepted (id %x)", id)
			}
			if id != ([32]byte{}) {
				t.Fatalf("rejected Submit returned id %x", id)
			}
			if got := f.log.names(); !reflect.DeepEqual(got, tc.wantCalls) {
				t.Fatalf("calls\n got %v\nwant %v", got, tc.wantCalls)
			}
			checkClass(t, err, tc.status, tc.kind, tc.reason)
			if got := len(f.guard.observedErrs()) - observedBefore; got != tc.observed {
				t.Fatalf("ObserveRejection calls = %d, want %d", got, tc.observed)
			}
			freezes := f.guard.freezeCauses()
			if tc.freeze == "" && len(freezes) != 0 {
				t.Fatalf("unexpected freeze %v", freezes)
			}
			if tc.freeze != "" && (len(freezes) != 1 || !strings.HasPrefix(freezes[0], tc.freeze)) {
				t.Fatalf("freezes = %v, want one with prefix %q", freezes, tc.freeze)
			}
			if got := f.store.count(); got != tc.rows {
				t.Fatalf("store rows = %d, want %d", got, tc.rows)
			}
		})
	}
}

// checkClass asserts the HTTP class of a Submit error as the api handler
// sees it (handlers_mining.go: ErrMiningUnavailable first, then RejectError).
func checkClass(t *testing.T, err error, status int, kind legacymining.RejectKind, reason mining.RejectReason) {
	t.Helper()
	if kind != 0 && legacymining.RejectKindOf(err) != kind {
		t.Fatalf("kind = %v, want %v (err %v)", legacymining.RejectKindOf(err), kind, err)
	}
	unavailable := errors.Is(err, api.ErrMiningUnavailable)
	switch status {
	case 503:
		if !unavailable {
			t.Fatalf("err %v does not wrap api.ErrMiningUnavailable", err)
		}
	case 400:
		var rej *mining.RejectError
		if unavailable || !errors.As(err, &rej) {
			t.Fatalf("err %v is not a 400 RejectError", err)
		}
		if reason != "" && rej.Reason != reason {
			t.Fatalf("reason = %q, want %q", rej.Reason, reason)
		}
	default:
		t.Fatalf("bad test status %d", status)
	}
}

// A proof refused at step 6 was never verified, so its ID is not claimed:
// once capacity frees up, the same bytes are accepted.
func TestHL1PendingFullDoesNotClaimProof(t *testing.T) {
	f := newHL1Fixture(t, 1, 0)
	f.guard.withMaxPending(1)
	svc := f.service(t)
	tip := svc.TipHeight()
	raw := newProofSolver(t, svc).solve(t, tip, headerAt(t, f.bp, tip), "qsdm1later")
	f.sink.setOutstanding(1)
	if _, err := svc.Submit(raw); legacymining.RejectKindOf(err) != legacymining.KindPendingFull {
		t.Fatalf("at cap: %v", err)
	}
	f.sink.setOutstanding(0)
	if _, err := svc.Submit(raw); err != nil {
		t.Fatalf("after capacity freed: %v", err)
	}
}

// A store failure freezes the guard, and a frozen guard closes admission for
// both routes.
func TestHL1AcceptIOFreezeClosesAdmission(t *testing.T) {
	f := newHL1Fixture(t, 1, 0)
	svc := f.service(t)
	tip := svc.TipHeight()
	ps := newProofSolver(t, svc)
	f.store.acceptErr = errors.New("database disk image is malformed")
	if _, err := svc.Submit(ps.solve(t, tip, headerAt(t, f.bp, tip), "qsdm1io")); !errors.Is(err, api.ErrMiningUnavailable) {
		t.Fatalf("I/O error: %v", err)
	}
	if _, err := svc.WorkAt(tip); !errors.Is(err, api.ErrMiningUnavailable) {
		t.Fatalf("WorkAt after freeze: %v", err)
	}
	f.store.acceptErr = nil
	if _, err := svc.Submit(ps.solve(t, tip, headerAt(t, f.bp, tip), "qsdm1after")); legacymining.RejectKindOf(err) != legacymining.KindAdmissionClosed {
		t.Fatalf("Submit after freeze: %v", err)
	}
}

// ---- HTTP mapping -----------------------------------------------------------

func TestHL1SubmitHTTPStatus(t *testing.T) {
	f := newHL1Fixture(t, 1, 0)
	svc := f.service(t)
	api.SetMiningService(svc)
	t.Cleanup(func() { api.SetMiningService(nil) })
	tip := svc.TipHeight()
	ps := newProofSolver(t, svc)
	post := func(body []byte) (*httptest.ResponseRecorder, api.MiningSubmitResponse) {
		rec := httptest.NewRecorder()
		(&api.Handlers{}).MiningSubmitHandler(rec, httptest.NewRequest(http.MethodPost, "/api/v1/mining/submit", bytes.NewReader(body)))
		var resp api.MiningSubmitResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec, resp
	}

	raw := ps.solve(t, tip, headerAt(t, f.bp, tip), "qsdm1http")
	if rec, resp := post(raw); rec.Code != http.StatusOK || !resp.Accepted {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	// Restart: fresh verifier state, same store.
	restarted := f.service(t)
	api.SetMiningService(restarted)
	if rec, resp := post(raw); rec.Code != http.StatusBadRequest || resp.RejectReason != string(mining.ReasonDuplicate) {
		t.Fatalf("duplicate after restart: %d %s", rec.Code, rec.Body)
	}
	f.guard.withMaxPending(1)
	if rec, _ := post(ps.solve(t, tip, headerAt(t, f.bp, tip), "qsdm1full")); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("pending full: %d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	f.guard.withMaxPending(legacymining.MaxPendingLimit)
	f.store.acceptErr = errors.New("disk I/O error")
	if rec, _ := post(ps.solve(t, tip, headerAt(t, f.bp, tip), "qsdm1io")); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("store I/O: %d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if causes := f.guard.freezeCauses(); len(causes) != 1 || !strings.Contains(causes[0], "disk I/O error") {
		t.Fatalf("freeze causes %q, want one carrying the store error", causes)
	}
}

// ---- concurrency ------------------------------------------------------------

// 200 goroutines race distinct valid proofs against max_pending. The cap is
// checked under submitMu, so it is never exceeded.
func TestHL1PendingCapHoldsUnderConcurrency(t *testing.T) {
	const goroutines, maxPending = 200, 5
	f := newHL1Fixture(t, 1, 0)
	f.guard.withMaxPending(maxPending)
	// Widen the window between the cap check and Enqueue.
	f.store.delay = func() { time.Sleep(200 * time.Microsecond) }
	svc := f.service(t)
	tip := svc.TipHeight()
	hdr := headerAt(t, f.bp, tip)
	ps := newProofSolver(t, svc)
	proofs := make([][]byte, goroutines)
	for i := range proofs {
		proofs[i] = ps.solve(t, tip, hdr, fmt.Sprintf("qsdm1race%03d", i))
	}

	var accepted, full, other sync.Map
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range proofs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.Submit(proofs[i])
			switch {
			case err == nil:
				accepted.Store(i, true)
			case legacymining.RejectKindOf(err) == legacymining.KindPendingFull && errors.Is(err, api.ErrMiningUnavailable):
				full.Store(i, true)
			default:
				other.Store(i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	count := func(m *sync.Map) (n int) {
		m.Range(func(_, _ any) bool { n++; return true })
		return n
	}
	other.Range(func(k, v any) bool { t.Errorf("proof %v: unexpected error %v", k, v); return true })
	if a, fl := count(&accepted), count(&full); a != maxPending || fl != goroutines-maxPending {
		t.Fatalf("accepted=%d full=%d, want %d and %d", a, fl, maxPending, goroutines-maxPending)
	}
	f.sink.mu.Lock()
	maxSeen := f.sink.maxSeen
	f.sink.mu.Unlock()
	if maxSeen > maxPending || f.store.count() != maxPending {
		t.Fatalf("maxSeen=%d rows=%d, cap %d", maxSeen, f.store.count(), maxPending)
	}
}

// 200 goroutines submit the same proof: exactly one Accept.
func TestHL1SameProofRaceAcceptsOnce(t *testing.T) {
	const goroutines = 200
	f := newHL1Fixture(t, 1, 0)
	svc := f.service(t)
	tip := svc.TipHeight()
	raw := newProofSolver(t, svc).solve(t, tip, headerAt(t, f.bp, tip), "qsdm1same")
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted, duplicates := 0, 0
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Submit(raw)
			var rej *mining.RejectError
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.As(err, &rej) && rej.Reason == mining.ReasonDuplicate:
				duplicates++
			default:
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if accepted != 1 || duplicates != goroutines-1 || f.store.count() != 1 || len(f.sink.records()) != 1 {
		t.Fatalf("accepted=%d duplicates=%d rows=%d enqueued=%d", accepted, duplicates, f.store.count(), len(f.sink.records()))
	}
}
