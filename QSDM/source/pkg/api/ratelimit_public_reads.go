package api

import (
	"net/http"
	"strings"
)

const highFrequencyPublicReadLimitPerMinute = 600

// isHighFrequencyPublicRead reports public GET/HEAD routes that are expected
// to be polled by Hive, validators, explorers, and status widgets. These
// reads should not spend the small anonymous role bucket; the pre-auth
// RateLimiter still applies highFrequencyPublicReadLimitPerMinute.
func isHighFrequencyPublicRead(r *http.Request) bool {
	if r == nil {
		return false
	}
	return isHighFrequencyPublicReadPath(r.URL.Path, r.Method)
}

func isHighFrequencyPublicReadPath(path, method string) bool {
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}

	if path == "/api/v1/status" || path == "/api/v1/versions" || path == "/api/v1/chain/blocks" {
		return true
	}
	if path == "/api/v1/tasks" || path == "/api/v1/tasks/state" || path == "/api/v1/tasks/actions" {
		return true
	}
	return strings.HasPrefix(path, "/api/v1/tasks/")
}

func highFrequencyPublicReadLimit(path, method string) int {
	if !isHighFrequencyPublicReadPath(path, method) {
		return 0
	}
	return highFrequencyPublicReadLimitPerMinute
}