package submesh

import (
	"fmt"
)

func (m *DynamicSubmeshManager) hasSubmeshes() bool {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	return len(m.Submeshes) > 0
}

func checkPayloadAgainstLimit(ds *DynamicSubmesh, payload []byte) error {
	if ds == nil || ds.MaxPayloadBytes <= 0 {
		return nil
	}
	if len(payload) > ds.MaxPayloadBytes {
		return fmt.Errorf("%w: transaction size %d exceeds submesh %q max_tx_size %d", ErrSubmeshPayloadTooLarge, len(payload), ds.Name, ds.MaxPayloadBytes)
	}
	return nil
}

// EnforceWalletSendPolicy applies fee/geo routing and max_tx_size when any submesh is configured.
func (m *DynamicSubmeshManager) EnforceWalletSendPolicy(fee float64, geoTag string, signedTxJSON []byte) error {
	if !m.hasSubmeshes() {
		return nil
	}
	ds, err := m.RouteTransaction(fee, geoTag)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSubmeshNoRoute, err)
	}
	return checkPayloadAgainstLimit(ds, signedTxJSON)
}

// MatchP2POrReject applies the same routing and max_tx_size rules as the HTTP wallet send when submeshes exist.
// If no submeshes are configured, returns (nil, nil) and the caller may treat routing as optional (warn-only).
func (m *DynamicSubmeshManager) MatchP2POrReject(fee float64, geoTag string, rawPayload []byte) (*DynamicSubmesh, error) {
	if !m.hasSubmeshes() {
		return nil, nil
	}
	ds, err := m.RouteTransaction(fee, geoTag)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSubmeshNoRoute, err)
	}
	if err := checkPayloadAgainstLimit(ds, rawPayload); err != nil {
		return nil, err
	}
	return ds, nil
}

// EnforcePrivilegedLedgerPayloadCap applies the strictest positive max_tx_size among all submeshes
// for operations without fee/geotag routing (mint, token create). If no submesh sets a cap, passes.
func (m *DynamicSubmeshManager) EnforcePrivilegedLedgerPayloadCap(payload []byte) error {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	if len(m.Submeshes) == 0 {
		return nil
	}
	tightest := 0
	for _, ds := range m.Submeshes {
		if ds.MaxPayloadBytes <= 0 {
			continue
		}
		if tightest == 0 || ds.MaxPayloadBytes < tightest {
			tightest = ds.MaxPayloadBytes
		}
	}
	if tightest > 0 && len(payload) > tightest {
		return fmt.Errorf("%w: payload size %d exceeds strictest submesh max_tx_size %d", ErrSubmeshPayloadTooLarge, len(payload), tightest)
	}
	return nil
}
