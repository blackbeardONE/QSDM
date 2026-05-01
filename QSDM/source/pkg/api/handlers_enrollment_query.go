package api

// Mining-enrollment READ endpoint (v2 protocol §7).
// Companion to handlers_enrollment.go (which is write-only):
//
//	GET /api/v1/mining/enrollment/{node_id}
//
// Lets miners verify their bond is locked, lets watchers
// confirm a NodeID is currently slashable (i.e. has a live
// bond to drain), and lets ops introspect the on-chain
// registry without scraping consensus state directly.
//
// Why a dedicated endpoint instead of "just expose Lookup":
//
//   - HMACKey is technically public chain state but it is a
//     hot value for the operator. Omitting it from the HTTP
//     response is a least-privilege choice; clients that
//     genuinely need the key can read it from chain state via
//     the validator RPC (which is auth-gated).
//   - Clients want a stable, versioned wire shape independent
//     of the on-chain struct layout. EnrollmentRecordView is
//     that shape.
//   - The endpoint MUST behave gracefully when no registry is
//     wired — running this handler on a v1-only node returns
//     503 with a clear "v2 enrollment not configured on this
//     node" message rather than 404 (which would imply the
//     query reached the registry and the node_id was unknown).
//
// Stateful path: this handler is the only piece in pkg/api
// that needs READ access to the on-chain enrollment registry.
// The narrow EnrollmentRegistry interface (below) keeps that
// dependency surgical — pkg/api still does NOT import the
// concrete *enrollment.InMemoryState.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// EnrollmentRegistry is the narrow read-only interface this
// handler depends on. The concrete implementation is
// *enrollment.InMemoryState; the indirection here exists for
// the same reason MempoolSubmitter does — pkg/api stays
// focused on HTTP concerns and unit tests use a fake.
//
// Lookup must return (nil, nil) for "not found" (matching the
// InMemoryState contract). Returning a non-nil error means the
// underlying store is unhealthy; the handler maps it to 500.
//
// The returned record is a copy — callers cannot mutate
// registry state through the returned pointer.
type EnrollmentRegistry interface {
	Lookup(nodeID string) (*enrollment.EnrollmentRecord, error)
}

type enrollmentRegistryHolder struct {
	mu       sync.RWMutex
	registry EnrollmentRegistry
}

var enrollmentRegHolder = &enrollmentRegistryHolder{}

// SetEnrollmentRegistry installs (or removes, when reg==nil)
// the process-wide registry the GET handler uses. Validators
// call this once at startup. Calling again replaces the prior
// registry — process restarts get clean state.
func SetEnrollmentRegistry(reg EnrollmentRegistry) {
	enrollmentRegHolder.mu.Lock()
	defer enrollmentRegHolder.mu.Unlock()
	enrollmentRegHolder.registry = reg
}

func currentEnrollmentRegistry() EnrollmentRegistry {
	enrollmentRegHolder.mu.RLock()
	defer enrollmentRegHolder.mu.RUnlock()
	return enrollmentRegHolder.registry
}

// EnrollmentRecordView is the wire shape for the read
// endpoint. Field set is intentionally smaller than
// enrollment.EnrollmentRecord:
//
//   - HMACKey is omitted (least privilege; see file doc).
//   - Phase is added so clients don't have to derive it from
//     RevokedAtHeight == 0 themselves.
//
// Bumping or reordering fields here is a wire-format change.
// JSON tags are stable across releases.
type EnrollmentRecordView struct {
	NodeID                string `json:"node_id"`
	Owner                 string `json:"owner"`
	GPUUUID               string `json:"gpu_uuid"`
	StakeDust             uint64 `json:"stake_dust"`
	EnrolledAtHeight      uint64 `json:"enrolled_at_height"`
	RevokedAtHeight       uint64 `json:"revoked_at_height,omitempty"`
	UnbondMaturesAtHeight uint64 `json:"unbond_matures_at_height,omitempty"`

	// Phase is one of "active", "pending_unbond", "revoked".
	// Derived from RevokedAtHeight + StakeDust: a record with
	// RevokedAtHeight==0 is "active"; with stake remaining and
	// a maturity height in the future, "pending_unbond"; with
	// stake drained (e.g. fully slashed) but record retained,
	// "revoked". The string form is stable; clients can switch
	// on it without re-deriving from raw fields.
	Phase string `json:"phase"`

	// Slashable is true iff the record carries a non-zero
	// StakeDust AND has not yet matured for unbond release.
	// A "yes, you can post evidence and drain real stake"
	// signal for slash submitters.
	Slashable bool `json:"slashable"`
}

// EnrollmentViewFromRecord is the public alias for
// viewFromRecord used by the internal/dashboard package's
// enrollment-overview tile. Centralising the
// EnrollmentRecord → EnrollmentRecordView translation here
// keeps the v1 query handler, the v1 list handler, and the
// dashboard tile in lockstep — adding a field to
// EnrollmentRecordView only needs to be wired in one place.
func EnrollmentViewFromRecord(rec *enrollment.EnrollmentRecord) EnrollmentRecordView {
	return viewFromRecord(rec)
}

func viewFromRecord(rec *enrollment.EnrollmentRecord) EnrollmentRecordView {
	v := EnrollmentRecordView{
		NodeID:                rec.NodeID,
		Owner:                 rec.Owner,
		GPUUUID:               rec.GPUUUID,
		StakeDust:             rec.StakeDust,
		EnrolledAtHeight:      rec.EnrolledAtHeight,
		RevokedAtHeight:       rec.RevokedAtHeight,
		UnbondMaturesAtHeight: rec.UnbondMaturesAtHeight,
	}
	switch {
	case rec.Active():
		v.Phase = "active"
		v.Slashable = rec.StakeDust > 0
	case rec.StakeDust > 0:
		v.Phase = "pending_unbond"
		v.Slashable = true
	default:
		v.Phase = "revoked"
		v.Slashable = false
	}
	return v
}

// EnrollmentQueryHandler serves
// GET /api/v1/mining/enrollment/{node_id}.
//
// Mounted on the trailing-slash route prefix; we extract the
// node_id by stripping the prefix off r.URL.Path. Same shape
// as routeContract / routeBridgeLock above so the routing
// idiom stays consistent across this file.
func (h *Handlers) EnrollmentQueryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reg := currentEnrollmentRegistry()
	if reg == nil {
		// Distinguishable from 404. v1-only nodes never wire a
		// registry; clients should retry against a v2-aware peer.
		writeMiningUnavailable(w, "v2 enrollment registry not configured on this node")
		return
	}

	const prefix = "/api/v1/mining/enrollment/"
	rawID := strings.TrimPrefix(r.URL.Path, prefix)
	rawID = strings.TrimSuffix(rawID, "/")
	if rawID == "" || strings.Contains(rawID, "/") {
		http.Error(w, "node_id required as path component", http.StatusBadRequest)
		return
	}
	if len(rawID) > enrollment.MaxNodeIDLen {
		http.Error(w, "node_id too long", http.StatusBadRequest)
		return
	}

	rec, err := reg.Lookup(rawID)
	if err != nil {
		// Wraps any storage-layer crash. Currently InMemoryState
		// never returns a non-nil error here, but keeping the
		// branch makes the handler safe for future on-disk
		// implementations.
		if errors.Is(err, enrollment.ErrPayloadInvalid) {
			http.Error(w, "node_id invalid: "+err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "enrollment lookup failed: "+err.Error(),
			http.StatusInternalServerError)
		return
	}
	if rec == nil {
		http.Error(w, "no enrollment record for node_id", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(viewFromRecord(rec))
}
