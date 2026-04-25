package transaction

import (
	"bytes"
	"encoding/json"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wasm"
)

type storageAdapter struct {
	store func([]byte) error
	close func() error
}

func (a storageAdapter) StoreTransaction(tx []byte) error { return a.store(tx) }
func (a storageAdapter) Close() error                       { return a.close() }

// AdaptStorage wraps main's (or any) storage value that exposes StoreTransaction and Close.
func AdaptStorage(s interface {
	StoreTransaction(tx []byte) error
	Close() error
}) Storage {
	if s == nil {
		return nil
	}
	return storageAdapter{
		store: s.StoreTransaction,
		close: s.Close,
	}
}

// DispatchDeps carries dependencies for inbound pubsub transaction dispatch.
type DispatchDeps struct {
	Logger            *logging.Logger
	Msg               []byte
	DynamicManager    *submesh.DynamicSubmeshManager
	WasmSdk           *wasm.WASMSDK
	Consensus         *consensus.ProofOfEntanglement
	Storage           Storage
	NvidiaGate        *monitoring.NvidiaLockP2PGate
	Mesh3dValidator   *mesh3d.Mesh3DValidator
	QuarantineManager *quarantine.QuarantineManager
	ReputationManager *quarantine.ReputationManager
}

// DispatchInboundP2P routes a single pubsub payload to the correct handler:
//   - Envelope kind "qsdm_mesh3d_v1" → mesh3d / phase-3 path
//   - Otherwise JSON matching ParseTransaction → wallet / JSON transaction path
//
// When the same logical wallet transaction is published twice (raw JSON plus mesh companion wrapping
// the same JSON), the second ingress is dropped by shared tx-id dedupe (see wallet_ingress_dedupe.go).
//
// Unrecognized payloads are ignored (debug log only) so the topic can carry other JSON in future.
func DispatchInboundP2P(d DispatchDeps) {
	msg := bytes.TrimSpace(d.Msg)
	if len(msg) == 0 || d.Logger == nil {
		return
	}

	if tx, sub, err := ParsePhase3Wire(msg); err == nil {
		if d.Mesh3dValidator == nil {
			d.Logger.Warn("mesh3d wire message dropped: validator not initialized")
			return
		}
		HandlePhase3MeshTx(d.Logger, tx, sub, d.Mesh3dValidator, d.QuarantineManager, d.ReputationManager, d.Consensus, d.Storage, d.NvidiaGate)
		return
	}

	if json.Valid(msg) && msg[0] == '{' {
		if _, err := ParseTransaction(msg); err == nil {
			HandleTransaction(d.Logger, msg, d.DynamicManager, d.WasmSdk, d.Consensus, d.Storage, d.NvidiaGate)
			return
		}
	}

	d.Logger.Debug("ignored pubsub payload (not wallet tx or mesh3d wire)", "len", len(msg))
}
