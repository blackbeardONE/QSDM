package monitoring

import "sync/atomic"

var (
	submeshP2PRejectRoute          atomic.Uint64
	submeshP2PRejectSize         atomic.Uint64
	submeshAPIWalletRejectRoute  atomic.Uint64
	submeshAPIWalletRejectSize   atomic.Uint64
	submeshAPIPrivilegedRejectSize atomic.Uint64
)

// RecordSubmeshP2PRejectRoute counts libp2p txs dropped before validation (no fee/geotag route when submeshes are configured).
func RecordSubmeshP2PRejectRoute() {
	submeshP2PRejectRoute.Add(1)
}

// SubmeshP2PRejectRouteCount returns P2P submesh route rejects since process start.
func SubmeshP2PRejectRouteCount() uint64 {
	return submeshP2PRejectRoute.Load()
}

// RecordSubmeshP2PRejectSize counts libp2p txs dropped for exceeding max_tx_size on the matched submesh.
func RecordSubmeshP2PRejectSize() {
	submeshP2PRejectSize.Add(1)
}

// SubmeshP2PRejectSizeCount returns P2P submesh oversize rejects since process start.
func SubmeshP2PRejectSizeCount() uint64 {
	return submeshP2PRejectSize.Load()
}

// RecordSubmeshAPIWalletRejectRoute counts POST /wallet/send rejected with 422 (no submesh route).
func RecordSubmeshAPIWalletRejectRoute() {
	submeshAPIWalletRejectRoute.Add(1)
}

func SubmeshAPIWalletRejectRouteCount() uint64 {
	return submeshAPIWalletRejectRoute.Load()
}

// RecordSubmeshAPIWalletRejectSize counts POST /wallet/send rejected with 422 (max_tx_size).
func RecordSubmeshAPIWalletRejectSize() {
	submeshAPIWalletRejectSize.Add(1)
}

func SubmeshAPIWalletRejectSizeCount() uint64 {
	return submeshAPIWalletRejectSize.Load()
}

// RecordSubmeshAPIPrivilegedRejectSize counts mint/token-create rejected with 422 (strictest max_tx_size).
func RecordSubmeshAPIPrivilegedRejectSize() {
	submeshAPIPrivilegedRejectSize.Add(1)
}

func SubmeshAPIPrivilegedRejectSizeCount() uint64 {
	return submeshAPIPrivilegedRejectSize.Load()
}
