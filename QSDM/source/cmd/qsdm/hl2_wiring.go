package main

// HL2 WP-C wiring (hl1/HL2_PUBLIC_MODE_DESIGN.md §2 M1, §4 row C): the
// version 2 Guard's EnrollmentView over the consensus enrollment state, and
// the operator-key index the OwnerAuth reads.
//
// None of this runs for a version 1 config: hl1NewCanary builds exactly the
// HL1 Guard and Store then. Since HL2 WP-E a version 2 config boots in canary
// mode, and in public mode with require_operator_sig (S2,
// legacymining.CheckSupported). ModePublic takes the same path as a v2
// canary (HL2 WP-D: hl1BootConfig.Canary is "mode is not off").

import (
	"sort"
	"sync"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// hl2EnrollmentView adapts *enrollment.InMemoryState to
// legacymining.EnrollmentView.
//
// Lookup reads the live state on every call, so status and bond are always
// current. OwnerNodes needs an owner -> node index the state does not have.
// The index is rebuilt (one List walk, O(records)) whenever:
//   - the state's Count() differs from the count at the last build (a node
//     was added or swept; slash evidence also counts, which only costs a
//     spurious rebuild), or
//   - Lookup returns a record whose (node, owner) pair is not in the index
//     (a node re-enrolled to another owner without a count change).
//
// OwnerNodes re-reads every indexed node and drops any whose owner changed,
// so a stale index can only miss a node, never attribute one to the wrong
// owner; and Precheck always runs Lookup on the submitted node first, which
// repairs that miss. Enrollment is closed under HL1/HL2 (the I7 audit
// freezes on any enroll-family tx in a local block, and the producer appends
// no external blocks), so in practice the index is built once, after the
// boot restore.
type hl2EnrollmentView struct {
	state *enrollment.InMemoryState

	mu        sync.Mutex
	built     bool
	count     int
	nodeOwner map[string]string   // node_id -> owner
	byOwner   map[string][]string // owner -> sorted node_ids
}

var _ legacymining.EnrollmentView = (*hl2EnrollmentView)(nil)

func newHL2EnrollmentView(state *enrollment.InMemoryState) *hl2EnrollmentView {
	return &hl2EnrollmentView{state: state}
}

func hl2EnrollmentInfo(r *enrollment.EnrollmentRecord) legacymining.EnrollmentInfo {
	return legacymining.EnrollmentInfo{
		NodeID:       r.NodeID,
		Owner:        r.Owner,
		Active:       r.Active(),
		FullyBonded:  r.FullyBonded(),
		StakeDust:    r.StakeDust,
		RequiredDust: r.RequiredBondDust(),
		DeferredBond: r.NormalizedBondMode() == enrollment.BondModeMiningRewards,
	}
}

// refreshLocked rebuilds the index if the state's count moved or force is
// set. Call with v.mu held; it takes the state's lock (never the reverse).
func (v *hl2EnrollmentView) refreshLocked(force bool) {
	n := v.state.Count()
	if v.built && !force && n == v.count {
		return
	}
	nodeOwner := make(map[string]string)
	byOwner := make(map[string][]string)
	cursor := ""
	for {
		page := v.state.List(enrollment.ListOptions{Cursor: cursor, Limit: enrollment.MaxListLimit})
		for _, r := range page.Records {
			nodeOwner[r.NodeID] = r.Owner
			byOwner[r.Owner] = append(byOwner[r.Owner], r.NodeID)
		}
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	for _, ids := range byOwner {
		sort.Strings(ids)
	}
	v.nodeOwner, v.byOwner, v.count, v.built = nodeOwner, byOwner, n, true
}

// Refresh rebuilds the index now (after the boot restore).
func (v *hl2EnrollmentView) Refresh() {
	if v == nil || v.state == nil {
		return
	}
	v.mu.Lock()
	v.refreshLocked(true)
	v.mu.Unlock()
}

// Lookup implements legacymining.EnrollmentView.
func (v *hl2EnrollmentView) Lookup(nodeID string) (legacymining.EnrollmentInfo, bool) {
	if v == nil || v.state == nil {
		return legacymining.EnrollmentInfo{}, false
	}
	r, err := v.state.Lookup(nodeID)
	if err != nil || r == nil {
		return legacymining.EnrollmentInfo{}, false
	}
	v.mu.Lock()
	v.refreshLocked(false)
	if v.nodeOwner[r.NodeID] != r.Owner {
		v.refreshLocked(true)
	}
	v.mu.Unlock()
	return hl2EnrollmentInfo(r), true
}

// OwnerNodes implements legacymining.EnrollmentView.
func (v *hl2EnrollmentView) OwnerNodes(owner string) []legacymining.EnrollmentInfo {
	if v == nil || v.state == nil {
		return nil
	}
	v.mu.Lock()
	v.refreshLocked(false)
	ids := append([]string(nil), v.byOwner[owner]...)
	v.mu.Unlock()
	out := make([]legacymining.EnrollmentInfo, 0, len(ids))
	for _, id := range ids {
		r, err := v.state.Lookup(id)
		if err != nil || r == nil || r.Owner != owner {
			continue
		}
		out = append(out, hl2EnrollmentInfo(r))
	}
	return out
}

// Owners returns the set of owners with at least one record.
func (v *hl2EnrollmentView) Owners() map[string]bool {
	out := make(map[string]bool)
	if v == nil || v.state == nil {
		return out
	}
	v.mu.Lock()
	v.refreshLocked(false)
	for o := range v.byOwner {
		out[o] = true
	}
	v.mu.Unlock()
	return out
}

// hl2HydrateOperatorKeys is the version 2 operator-key boot step ("S14b"):
// after S7-S14 (the enrollment state and the chain are restored, the Store
// is open if reconciliation got that far) and before S15/S16. It indexes the
// key of every enrollment owner from the restored chain (any tx whose
// public_key hashes to its sender, normally the owner's qsdm/enroll/v2 tx),
// merges the operator_keys rows, and writes new keys back. Admission cannot
// open before S16, so no submission sees a partial index. With a version 1
// config, or without the canary machinery, it does nothing.
func hl2HydrateOperatorKeys(c *hl1CanaryParts, blocks []*chain.Block, storeOpen bool) legacymining.OperatorKeyReport {
	if !c.enabled() || c.keys == nil || c.view == nil {
		return legacymining.OperatorKeyReport{}
	}
	c.view.Refresh()
	owners := c.view.Owners()
	found := legacymining.OperatorKeysFromBlocks(blocks, func(o string) bool { return owners[o] })
	var store legacymining.OperatorKeyStore
	if storeOpen {
		store = c.store
	}
	rep, err := legacymining.HydrateOperatorKeys(c.keys, store, found)
	missing := 0
	for o := range owners {
		if _, ok := c.keys.Lookup(o); !ok {
			missing++
		}
	}
	log := hl1Logger()
	if err != nil {
		log.Error("hl2: operator keys: store step failed; using the keys found in memory",
			"error_str", err.Error())
	}
	if rep.BadRows != 0 {
		log.Error("hl2: operator keys: operator_keys rows whose key does not hash to the owner were ignored",
			"bad_rows", rep.BadRows)
	}
	log.Info("hl2: operator keys hydrated",
		"owners", len(owners), "with_key", len(owners)-missing, "without_key", missing,
		"from_store", rep.FromStore, "from_chain", rep.Found, "new", rep.New, "persisted", rep.Persisted,
		"require_operator_sig", c.guard != nil && c.guard.Config().RequireOperatorSig)
	return rep
}
