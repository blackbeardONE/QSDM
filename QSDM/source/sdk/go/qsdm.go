// qsdm.go adds preferred rebrand-era aliases to the Go SDK.
//
// The QSDM platform was historically shipped under the transitional name
// "QSDM+" and the Go package identifier qsdmplus was chosen accordingly. This
// file adds QSDMClient as the preferred alias for the canonical Client type
// and preserves QSDMPlusClient as a legacy alias during the deprecation
// window. New code should prefer QSDMClient.
//
// A separate sub-package, sdk/go/qsdm, re-exports the same surface under the
// new import path github.com/blackbeardONE/QSDM/sdk/go/qsdm so that consumers
// can migrate their import path on their own cadence.
//
// Native coin: Cell (CELL), 8 decimals, smallest unit "dust".

package qsdmplus

// QSDMClient is the preferred name for Client. The wire protocol, authentication
// model, and method surface are identical. Both names refer to the same type,
// so you can freely cast or pass between variables of either declared type.
type QSDMClient = Client

// QSDMPlusClient is a legacy alias retained for the rebrand deprecation window.
// New code should prefer QSDMClient.
type QSDMPlusClient = Client

// NewQSDMClient is a preferred-name convenience constructor equivalent to
// NewClient. It returns a *Client because QSDMClient is a type alias.
func NewQSDMClient(baseURL string) *Client { return NewClient(baseURL) }
