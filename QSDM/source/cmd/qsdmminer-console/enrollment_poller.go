package main

// enrollment_poller.go — background poller that watches the
// operator's on-chain enrollment record and surfaces phase
// changes (active → pending_unbond → revoked → not_found) as
// Events the runLoop's existing Dashboard already knows how to
// paint.
//
// Why this lives in the miner binary rather than as a
// peer-of-v2client helper:
//
//   - Its only consumer is the console miner's Dashboard. A
//     command-line operator running `qsdmcli enrollment-status`
//     gets the same data on demand; the poller's job is to
//     surface it AT ALL TIMES while the miner is running.
//   - The wire shape it depends on (api.EnrollmentRecordView)
//     is already mirrored at v2client/v2client.go:50 for the
//     same reason — pulling pkg/api into miner binaries would
//     drag the whole HTTP server tree into the link graph for
//     a single struct.
//
// What this poller is NOT:
//
//   - Not a consensus participant. It only READS chain state
//     via /api/v1/mining/enrollment/{node_id}; nothing it
//     observes ever feeds back into the proof pipeline.
//   - Not a slasher. It surfaces "you got slashed" so the
//     operator can react; whether to slash someone else is the
//     job of `qsdmcli slash`, not the miner.
//   - Not authoritative. The validator the miner is talking to
//     is one node on the network. A revoked record on this
//     validator means "the chain this validator is following
//     thinks you're revoked", which on a healthy network is
//     equivalent to "you're revoked" but is NOT a substitute
//     for cross-validator confirmation.
//
// Polling cadence: 30 seconds by default. With N miners on a
// validator that's N/30 RPS of read traffic — the
// EnrollmentQueryHandler is a hot in-memory map lookup so even
// 1k miners is sub-1k QPS. Operators can tighten or loosen via
// --enrollment-poll. Setting --enrollment-poll=0 disables the
// poller entirely (useful when the validator advertises the
// 503 "v2 not configured" surface and the operator just wants
// to suppress the spam).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultEnrollmentPollInterval is the cadence at which a
// running miner re-fetches its enrollment record. Chosen to
// match the median wall-clock latency operators are willing to
// wait between cause (e.g. a slash transaction lands) and
// effect (the miner shows pending_unbond). A tighter interval
// adds load to the validator's read path; a looser interval
// extends the "did the slash actually land?" suspense window.
const DefaultEnrollmentPollInterval = 30 * time.Second

// MinEnrollmentPollInterval is the floor below which we refuse
// to schedule the poller. 5s lets a determined operator
// debugging an enrollment race iterate fast without making the
// miner mass-DDoS the validator on a fork. Anything below this
// is silently rounded UP — we'd rather a misconfigured operator
// run slightly slow than have a stuck miner produce a measurable
// spike on the validator's request graph.
const MinEnrollmentPollInterval = 5 * time.Second

// EnrollmentPhase mirrors the validator's wire `phase` enum
// PLUS two miner-side states the validator never emits:
//
//   - PhaseUnknown: the poller has not yet succeeded once, OR
//     the validator returned a non-2xx that doesn't fit a known
//     bucket. Distinct from PhaseNotFound so the dashboard can
//     show "—" vs "not enrolled".
//   - PhaseNotFound: validator returned 404. The most actionable
//     state for a fresh operator: "your enroll tx hasn't been
//     mined yet, or you typed the node_id wrong."
type EnrollmentPhase string

const (
	PhaseUnknown    EnrollmentPhase = "unknown"
	PhaseActive     EnrollmentPhase = "active"
	PhasePending    EnrollmentPhase = "pending_unbond"
	PhaseRevoked    EnrollmentPhase = "revoked"
	PhaseNotFound   EnrollmentPhase = "not_found"
	PhaseUnconfig   EnrollmentPhase = "unconfigured" // validator is v1-only / 503
)

// EnrollmentStatus is the merged view of one poll cycle. The
// runLoop's dashboard renders this verbatim, the test suite
// asserts against it field-by-field, and every other consumer
// of the poller (event emitter, log line) reads only from it.
//
// Zero value is a valid "we haven't polled yet" state and renders
// as "—" in the dashboard. The Phase field is always populated
// after the first cycle.
type EnrollmentStatus struct {
	NodeID                string
	Phase                 EnrollmentPhase
	StakeDust             uint64
	Slashable             bool
	EnrolledAtHeight      uint64
	RevokedAtHeight       uint64
	UnbondMaturesAtHeight uint64

	// LastPolledAt is the wall-clock time of the most recent
	// completed cycle (success OR failure). Drives the "polled
	// Ns ago" element in the dashboard row.
	LastPolledAt time.Time

	// LastError is empty on a successful poll and the trimmed
	// HTTP-or-network error message otherwise. Operators read
	// this to disambiguate "validator unreachable" from
	// "validator says you're not enrolled."
	LastError string
}

// enrollmentRecordWire is the on-the-wire shape of
// pkg/api/handlers_enrollment_query.go's EnrollmentRecordView.
// We deliberately mirror it locally rather than import pkg/api
// to keep the miner binary's link graph minimal — the same
// pattern v2client.challengeWire uses for the challenge endpoint.
//
// JSON tags MUST stay byte-identical to api.EnrollmentRecordView;
// TestEnrollmentRecordWireMatchesAPI in enrollment_poller_test.go
// asserts this at build-with-tests time.
type enrollmentRecordWire struct {
	NodeID                string `json:"node_id"`
	Owner                 string `json:"owner"`
	GPUUUID               string `json:"gpu_uuid"`
	StakeDust             uint64 `json:"stake_dust"`
	EnrolledAtHeight      uint64 `json:"enrolled_at_height"`
	RevokedAtHeight       uint64 `json:"revoked_at_height,omitempty"`
	UnbondMaturesAtHeight uint64 `json:"unbond_matures_at_height,omitempty"`
	Phase                 string `json:"phase"`
	Slashable             bool   `json:"slashable"`
}

// EnrollmentPoller holds the immutable inputs of a poll cycle.
// One instance per running miner; the goroutine spun up by Run
// owns the only mutating reference to the prior phase (used to
// detect phase transitions).
//
// Concurrency: PollOnce is safe to call concurrently with Run
// (each call uses its own *http.Request), but Run is intended
// to be the only long-running consumer.
type EnrollmentPoller struct {
	// Client is the http.Client used for GET requests. REQUIRED.
	// Reusing the runLoop's client share's its timeout / TLS
	// settings, which is the correct posture (operators don't
	// want a separate timeout policy for enrollment polls).
	Client *http.Client

	// BaseURL is the validator base, e.g. "https://val.qsdm.tech".
	// REQUIRED. Trailing slash tolerated.
	BaseURL string

	// NodeID is the operator's enrolled handle. REQUIRED. The
	// poller refuses to start with an empty NodeID — running with
	// no node_id would query "/api/v1/mining/enrollment/" which
	// the handler 400s, producing endless "polled but unknown"
	// noise.
	NodeID string

	// Interval is the time between poll cycles. Zero or below
	// MinEnrollmentPollInterval is rounded up to
	// DefaultEnrollmentPollInterval (zero) or
	// MinEnrollmentPollInterval (positive but too small).
	Interval time.Duration

	// OnStatus is called after every cycle (success OR failure)
	// with the resulting EnrollmentStatus. Optional — the
	// runLoop wires it to a callback that emits an EvEnrollment
	// event into the dashboard's event channel. Nil = drop.
	OnStatus func(EnrollmentStatus)

	// OnPhaseChange is called ONLY when the phase transitions
	// between successful cycles. Carries (prev, next) so the
	// callback can decide which transitions deserve a louder
	// log line (e.g. active → pending_unbond is the auto-revoke
	// signal; pending_unbond → active is the operator un-doing
	// a manual unbond, which is fine to whisper). Nil = drop.
	OnPhaseChange func(prev, next EnrollmentStatus)

	// LogError is called when a poll cycle's HTTP request fails.
	// Optional. The poller does NOT abort on errors; transient
	// validator outages just mean LastError is set on the next
	// status update.
	LogError func(error)
}

// NewEnrollmentPoller validates inputs and returns a poller
// ready for Run / PollOnce. Returns an error rather than
// silently substituting defaults for the consensus-shaped
// fields (BaseURL, NodeID, Client) because every one of those
// being wrong is an operator-visible bug we want surfaced at
// startup, not first-poll-time.
func NewEnrollmentPoller(
	client *http.Client, baseURL, nodeID string, interval time.Duration,
) (*EnrollmentPoller, error) {
	if client == nil {
		return nil, errors.New("enrollment-poller: nil http client")
	}
	if baseURL == "" {
		return nil, errors.New("enrollment-poller: baseURL must not be empty")
	}
	if nodeID == "" {
		return nil, errors.New("enrollment-poller: nodeID must not be empty")
	}
	// Trailing slashes corrupt the path-join below. Caller
	// mistakes (e.g. validator_url="https://x/") shouldn't
	// silently produce 404s on the validator that strips the
	// extra segment.
	baseURL = strings.TrimRight(baseURL, "/")
	if interval == 0 {
		interval = DefaultEnrollmentPollInterval
	}
	if interval < MinEnrollmentPollInterval {
		interval = MinEnrollmentPollInterval
	}
	return &EnrollmentPoller{
		Client:   client,
		BaseURL:  baseURL,
		NodeID:   nodeID,
		Interval: interval,
	}, nil
}

// Run is the long-running poll loop. Polls immediately on
// entry (so the dashboard's first paint is informative), then
// every Interval until ctx is cancelled. Returns on ctx
// cancellation; never panics.
//
// Phase-transition detection is keyed on the LAST SUCCESSFUL
// cycle's phase, NOT the last attempted cycle. A transient
// 503 "validator returned 5xx" doesn't count as transitioning
// to PhaseUnknown — that would create false-positive "you got
// revoked!" events every time the validator hiccups. The
// observed phase only updates when the wire payload is
// actually decodable.
func (p *EnrollmentPoller) Run(ctx context.Context) {
	if p == nil {
		return
	}
	var lastSuccess EnrollmentStatus
	hasLast := false

	tick := func() {
		st := p.PollOnceWithContext(ctx)
		if p.OnStatus != nil {
			p.OnStatus(st)
		}
		if st.LastError != "" && p.LogError != nil {
			p.LogError(errors.New(st.LastError))
		}
		// Only treat a cycle as "successful for phase
		// transition purposes" if the validator returned
		// a phase we recognise. PhaseUnknown / PhaseUnconfig
		// (the ambient "we don't know" states) do not flip
		// the prev marker — otherwise a momentarily-down
		// validator would amplify into a stream of "phase
		// transitioned to unknown" events.
		switch st.Phase {
		case PhaseActive, PhasePending, PhaseRevoked, PhaseNotFound:
			if hasLast && lastSuccess.Phase != st.Phase {
				if p.OnPhaseChange != nil {
					p.OnPhaseChange(lastSuccess, st)
				}
			}
			lastSuccess = st
			hasLast = true
		}
	}

	tick()
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

// PollOnce performs one cycle and returns the resulting
// status. Public for test harnesses; production code should use
// Run, which manages cadence + transition detection.
func (p *EnrollmentPoller) PollOnce(ctx context.Context) EnrollmentStatus {
	return p.PollOnceWithContext(ctx)
}

// PollOnceWithContext is the internal cycle. The Run loop
// passes its own ctx so cancellation propagates immediately —
// without this, a slow validator could keep the poll loop
// hanging for the full http.Client.Timeout after Run is told
// to stop.
func (p *EnrollmentPoller) PollOnceWithContext(ctx context.Context) EnrollmentStatus {
	st := EnrollmentStatus{
		NodeID:       p.NodeID,
		Phase:        PhaseUnknown,
		LastPolledAt: time.Now(),
	}

	endpoint := fmt.Sprintf("%s/api/v1/mining/enrollment/%s",
		p.BaseURL, url.PathEscape(p.NodeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		st.LastError = "build request: " + err.Error()
		return st
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.Client.Do(req)
	if err != nil {
		st.LastError = "http: " + err.Error()
		return st
	}
	defer resp.Body.Close()

	// 4 KiB is ~10× the size of a real EnrollmentRecordView. A
	// misbehaving validator shouldn't be able to wedge the miner
	// by streaming a multi-megabyte response on this read path.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		st.LastError = "read body: " + err.Error()
		return st
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// continue below
	case http.StatusNotFound:
		st.Phase = PhaseNotFound
		return st
	case http.StatusServiceUnavailable:
		// 503 is the "v2 enrollment not configured on this
		// node" surface (handlers_enrollment_query.go:161).
		// Treating this as PhaseUnconfig (vs Unknown) lets the
		// dashboard render a clear "this validator does not
		// support v2 reads" message rather than a generic
		// "polling failed."
		st.Phase = PhaseUnconfig
		st.LastError = "validator: " + truncateForLine(string(body), 120)
		return st
	default:
		st.LastError = fmt.Sprintf("validator returned %d: %s",
			resp.StatusCode, truncateForLine(string(body), 120))
		return st
	}

	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	var w enrollmentRecordWire
	if err := dec.Decode(&w); err != nil {
		st.LastError = "decode: " + err.Error()
		return st
	}

	st.StakeDust = w.StakeDust
	st.EnrolledAtHeight = w.EnrolledAtHeight
	st.RevokedAtHeight = w.RevokedAtHeight
	st.UnbondMaturesAtHeight = w.UnbondMaturesAtHeight
	st.Slashable = w.Slashable

	// Map wire phase → typed phase. Unknown wire strings fall
	// back to Unknown (NOT Active) so a future validator
	// extension that adds a new phase the miner doesn't
	// understand is surfaced as "go look at this", not silently
	// painted as "all good."
	switch w.Phase {
	case "active":
		st.Phase = PhaseActive
	case "pending_unbond":
		st.Phase = PhasePending
	case "revoked":
		st.Phase = PhaseRevoked
	default:
		st.Phase = PhaseUnknown
		st.LastError = fmt.Sprintf("validator returned unknown phase %q", w.Phase)
	}

	return st
}

// PhaseTransitionSeverity classifies a phase transition for
// the dashboard's coloring + logging. The runLoop maps the
// returned severity to an EventKind:
//
//   - SeverityInfo   → EvInfo  (e.g. not_found → active means
//     "your enrollment landed", which is a happy event)
//   - SeverityWarn   → EvError (e.g. active → pending_unbond
//     could be either a manual unbond OR an auto-revoke; both
//     warrant a louder log line)
//   - SeverityErr    → EvError (revoked is terminal; whatever
//     got us here is operator-visible and the miner should
//     surface it loudly)
//
// Pure function, easy to unit-test alongside the poller.
type PhaseTransitionSeverity int

const (
	SeverityInfo PhaseTransitionSeverity = iota
	SeverityWarn
	SeverityErr
)

// SeverityForTransition maps (prev → next) to a severity
// bucket. Centralising the table here keeps the runLoop
// integration trivial and gives the test suite one place to
// pin the "did we lose / gain stakeable status?" semantics.
func SeverityForTransition(prev, next EnrollmentPhase) PhaseTransitionSeverity {
	switch {
	// First-time landing on chain — operator just enrolled.
	case prev == PhaseNotFound && next == PhaseActive:
		return SeverityInfo
	// Benign exit path. The operator chose to unbond; this is
	// expected behaviour.
	case prev == PhaseActive && next == PhasePending:
		return SeverityWarn
	// Bad: stake fully drained or unbond matured. Both end at
	// "you can't mine anymore" so they're alarmable.
	case next == PhaseRevoked:
		return SeverityErr
	// Bad: chain forgot us. Shouldn't happen but if it does,
	// surface it loudly.
	case next == PhaseNotFound:
		return SeverityErr
	default:
		// Any other transition (including unbond → active, an
		// edge case where ops re-bonded before the unbond
		// matured — possible if a future protocol revision
		// adds a "cancel-unbond" path) is informational.
		return SeverityInfo
	}
}
