package storage

import stderrors "errors"

// ErrAtomicTransferUnsupported is returned by ApplyTransferAtomic on a backend
// that cannot settle a transfer atomically.
//
// Declared here, without a build tag, because the other storage sentinels are
// duplicated across sqlite_v041.go (cgo) and sqlite_stub.go (!cgo) and this one
// is meaningful to backends in both builds.
//
// Why it exists: both wallet write endpoints settle through ApplyTransferAtomic,
// and only SQLite implements it. FileStorage refuses by construction and
// ScyllaStorage is still a stub (scylla.go, v0.4.1 §3.2 CQL LWT pending). Before
// this sentinel those two returned bare fmt.Errorf values, so the handler could
// not tell "this backend cannot do transfers at all" from "this transfer
// failed", and reported both as a generic 500. An operator on a file- or
// Scylla-backed node saw an opaque server error with no indication that the
// endpoint could never work there.
//
// Callers should map this to a distinct status: the request is not malformed and
// retrying will not help, so it is a capability gap, not a failure.
var ErrAtomicTransferUnsupported = stderrors.New("storage: backend does not support atomic transfers")
