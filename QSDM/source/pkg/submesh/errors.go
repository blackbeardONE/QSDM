package submesh

import "errors"

var (
	// ErrSubmeshNoRoute is returned when fee/geotag do not match any configured submesh (with submeshes present).
	ErrSubmeshNoRoute = errors.New("no matching submesh for fee and geotag")
	// ErrSubmeshPayloadTooLarge is returned when serialized payload exceeds max_tx_size (routed wallet/P2P or privileged cap).
	ErrSubmeshPayloadTooLarge = errors.New("payload exceeds submesh max_tx_size")
)
