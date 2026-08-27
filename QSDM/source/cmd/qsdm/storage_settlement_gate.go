package main

import "fmt"

// SettlementCapability describes what a chosen storage backend can do about
// wallet transfers, and what the node should say and do about it.
//
// This exists because the same three-part decision -- log it, gate on the
// operator's opt-in, set the health status -- was copy-pasted per storage
// branch in main.go, and I got it wrong once per paste. Two Scylla-side
// branches were fixed in 3dd199d; a reviewer then found a THIRD, on the
// non-Scylla path where SQLite init fails and the node falls back to file
// storage. That branch logged nothing, gated nothing, and reported the
// component HEALTHY for a backend that cannot settle a transfer at all.
//
// It is also the DEFAULT path: `CGO_ENABLED=0` builds have no SQLite
// (sqlite.go is //go:build cgo) and several shipped scripts build that way
// (scripts/go-build-no-cgo.sh, scripts/ci-local-parity.sh,
// deploy/scripts/build_hive_linux.sh). So the branch nobody described was the
// one most nodes take.
//
// Deciding once and calling it three times is the fix. A fourth backend gets
// the behaviour by construction rather than by my remembering to paste it.
type SettlementCapability struct {
	// CanSettle is false when the backend's ApplyTransferAtomic cannot
	// complete a transfer -- FileStorage refuses by construction,
	// ScyllaStorage is an unimplemented stub. Only SQLite returns true today.
	CanSettle bool
	// Backend names the concrete backend for operator-facing messages.
	Backend string
	// Reason is the specific mechanism, so the log points at code rather than
	// restating the category.
	Reason string
}

// FatalMessage returns the message to abort startup with, or "" to continue.
// Aborting is opt-in: requireSettleable comes from
// QSDM_REQUIRE_SETTLEABLE_STORAGE, default off, because refusing to boot would
// break every deployment that never touches wallet writes.
func (c SettlementCapability) FatalMessage(requireSettleable bool) string {
	if c.CanSettle || !requireSettleable {
		return ""
	}
	return fmt.Sprintf("QSDM_REQUIRE_SETTLEABLE_STORAGE=1 and the selected backend (%s) cannot "+
		"settle transfers: %s", c.Backend, c.Reason)
}

// OperatorWarning returns the startup line explaining the consequence, or ""
// when the backend can settle.
func (c SettlementCapability) OperatorWarning() string {
	if c.CanSettle {
		return ""
	}
	return fmt.Sprintf("STORAGE BACKEND CANNOT SETTLE TRANSFERS: %s -- %s. Both wallet write "+
		"endpoints settle through ApplyTransferAtomic, so /wallet/send and /wallet/submit-signed "+
		"return 501 on this node. SQLite is the only backend that implements atomic transfers "+
		"today. Set QSDM_REQUIRE_SETTLEABLE_STORAGE=1 to refuse to start instead of serving an "+
		"endpoint that cannot work.", c.Backend, c.Reason)
}

// HealthDetail returns the storage health message. Paired with HealthDegraded
// so the two cannot drift apart -- the third branch reported "Healthy" beside a
// backend that could not settle, which is the specific lie this pairing
// prevents.
func (c SettlementCapability) HealthDetail() string {
	if c.CanSettle {
		return fmt.Sprintf("%s storage initialized", c.Backend)
	}
	return fmt.Sprintf("%s storage initialized, but it cannot settle transfers: %s", c.Backend, c.Reason)
}

// HealthDegraded reports whether the storage component should be Degraded.
func (c SettlementCapability) HealthDegraded() bool { return !c.CanSettle }

// The three backends this node can select, named once.
var (
	settlementSQLite = SettlementCapability{CanSettle: true, Backend: "SQLite"}
	settlementScylla = SettlementCapability{
		Backend: "ScyllaDB",
		Reason:  "ApplyTransferAtomic is an unimplemented stub (scylla.go, v0.4.1 §3.2 CQL LWT pending)",
	}
	settlementFile = SettlementCapability{
		Backend: "file",
		Reason:  "FileStorage.ApplyTransferAtomic refuses by construction (file_storage.go)",
	}
)
