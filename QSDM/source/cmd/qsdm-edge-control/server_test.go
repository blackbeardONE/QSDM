package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlServerRequiresSession(t *testing.T) {
	paths := testControlPaths(t, "server")
	control := newController(paths, defaultSettings(), "test")
	control.autoStart = func(bool, string) error { return nil }
	server, err := newControlServer(control, "0123456789abcdef0123456789abcdef", make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	unauthorizedRecorder := httptest.NewRecorder()
	server.handler().ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedRecorder.Code)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	authorized.Header.Set("X-QSDM-Edge-Token", "0123456789abcdef0123456789abcdef")
	authorizedRecorder := httptest.NewRecorder()
	server.handler().ServeHTTP(authorizedRecorder, authorized)
	if authorizedRecorder.Code != http.StatusOK {
		t.Fatalf("authorized status = %d; body=%s", authorizedRecorder.Code, authorizedRecorder.Body.String())
	}
}

func TestControlServerExchangesLaunchTokenForCookie(t *testing.T) {
	paths := testControlPaths(t, "cookie")
	control := newController(paths, defaultSettings(), "test")
	server, err := newControlServer(control, "0123456789abcdef0123456789abcdef", make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/?t=0123456789abcdef0123456789abcdef", nil)
	recorder := httptest.NewRecorder()
	server.handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected session cookie: %+v", cookies)
	}
}
