package main

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

type gatewayHandler struct {
	backend         *url.URL
	allowEnrollment bool
	allowHive       bool
	proxy           *httputil.ReverseProxy
}

func newGatewayHandler(backend *url.URL, allowEnrollment bool, allowHive bool) *gatewayHandler {
	g := &gatewayHandler{
		backend:         backend,
		allowEnrollment: allowEnrollment,
		allowHive:       allowHive,
	}
	g.proxy = httputil.NewSingleHostReverseProxy(backend)
	origDirector := g.proxy.Director
	g.proxy.Director = func(r *http.Request) {
		origDirector(r)
		r.Host = backend.Host
	}
	g.proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "local validator unavailable", http.StatusBadGateway)
	}
	return g
}

func (g *gatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-QSDM-Gateway", "home")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")

	if !g.allowed(r.Method, r.URL.Path) {
		http.Error(w, "route not exposed by qsdm-home-gateway", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	g.proxy.ServeHTTP(w, r)
}

func (g *gatewayHandler) allowed(method, path string) bool {
	if strings.Contains(path, "..") {
		return false
	}

	if method == http.MethodGet {
		switch path {
		case "/api/v1/status",
			"/api/v1/health",
			"/api/v1/health/live",
			"/api/v1/health/ready",
			"/api/v1/mining/work",
			"/api/v1/mining/challenge",
			"/api/v1/mining/emission",
			"/api/v1/mining/blocks",
			"/api/v1/chain/blocks":
			return true
		}
		if strings.HasPrefix(path, "/api/v1/mining/enrollment/") {
			return true
		}
	}

	if method == http.MethodPost && path == "/api/v1/mining/submit" {
		return true
	}

	if g.allowHive && hiveConsumerRoute(method, path) {
		return true
	}

	if g.allowEnrollment {
		if method == http.MethodGet && path == "/api/v1/mining/enrollments" {
			return true
		}
		if method == http.MethodPost && (path == "/api/v1/mining/enroll" || path == "/api/v1/mining/unenroll") {
			return true
		}
	}

	return false
}

func hiveConsumerRoute(method, path string) bool {
	if method == http.MethodGet {
		switch path {
		case "/api/v1/versions",
			"/api/v1/wallet/balance",
			"/api/v1/wallet/nonce",
			"/api/v1/mining/account",
			"/api/v1/receipts",
			"/api/v1/tasks",
			"/api/v1/tasks/state",
			"/api/v1/tasks/actions":
			return true
		}
		if strings.HasPrefix(path, "/api/v1/receipts/") {
			return true
		}
		if strings.HasPrefix(path, "/api/v1/tasks/") {
			return true
		}
	}

	if method == http.MethodPost {
		switch path {
		case "/api/v1/wallet/submit-signed",
			"/api/v1/tasks/actions/submit-signed":
			return true
		}
	}

	return false
}
