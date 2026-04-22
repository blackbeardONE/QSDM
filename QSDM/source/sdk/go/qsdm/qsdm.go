// Package qsdm is the preferred Go client package for the QSDM HTTP API.
//
// The QSDM platform was historically shipped under the transitional name
// "QSDM+" and the original Go SDK lives at sdk/go with package identifier
// qsdmplus. This sub-package re-exports the same surface under the preferred
// import path github.com/blackbeardONE/QSDM/sdk/go/qsdm so that consumers can
// migrate their imports independently of a breaking API change.
//
// Usage:
//
//	import "github.com/blackbeardONE/QSDM/sdk/go/qsdm"
//
//	c := qsdm.NewClient("http://node:8080")
//	c.SetToken(jwt)
//	bal, err := c.GetBalance("addr")
//
// The wire protocol is identical to the legacy package. Native coin: Cell
// (CELL), 8 decimals, smallest unit "dust".
package qsdm

import (
	"context"

	qsdmplus "github.com/blackbeardONE/QSDM/sdk/go"
)

// Client is the preferred name for the QSDM HTTP API client.
type Client = qsdmplus.Client

// QSDMClient is a verbose alias of Client retained for symmetry with the JS
// SDK. Both names refer to the same underlying type.
type QSDMClient = qsdmplus.Client

// HealthStatus mirrors the legacy package type.
type HealthStatus = qsdmplus.HealthStatus

// NodeStatus mirrors the legacy package type.
type NodeStatus = qsdmplus.NodeStatus

// CoinInfo mirrors the legacy package type (coin metadata from /api/v1/status).
type CoinInfo = qsdmplus.CoinInfo

// BrandInfo mirrors the legacy package type (branding metadata from /api/v1/status).
type BrandInfo = qsdmplus.BrandInfo

// TokenomicsInfo mirrors the legacy package type (live emission snapshot
// from /api/v1/status).
type TokenomicsInfo = qsdmplus.TokenomicsInfo

// ErrAPI mirrors the legacy package error type. Callers can use errors.As to
// extract the status code and response body for diagnostics.
type ErrAPI = qsdmplus.ErrAPI

// NewClient creates a new QSDM API client with a 30s default timeout. It is
// equivalent to qsdmplus.NewClient.
func NewClient(baseURL string) *Client { return qsdmplus.NewClient(baseURL) }

// IsNotFound reports whether err is a 404 API error.
func IsNotFound(err error) bool { return qsdmplus.IsNotFound(err) }

// IsUnauthorized reports whether err is a 401/403 API error.
func IsUnauthorized(err error) bool { return qsdmplus.IsUnauthorized(err) }

// Compile-time assertion that this package mirrors the legacy surface.
var (
	_ = NewClient
	_ = IsNotFound
	_ = IsUnauthorized
	_ context.Context
)
