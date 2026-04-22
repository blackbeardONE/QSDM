package webviewer

import (
	"errors"
	"testing"
)

// The credential policy is the only behavior in this package that is
// security-sensitive — a regression here re-opens the admin/password
// foot-gun that the v1.x public release introduced. Lock it down.

func TestResolveCreds_BothSet_ReturnsEnvValues(t *testing.T) {
	t.Setenv("WEBVIEWER_USERNAME", "alice")
	t.Setenv("WEBVIEWER_PASSWORD", "correct-horse-battery-staple")
	t.Setenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS", "")

	user, pass, err := resolveCreds()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "alice" {
		t.Fatalf("username: got %q, want %q", user, "alice")
	}
	if pass != "correct-horse-battery-staple" {
		t.Fatalf("password not returned verbatim from env")
	}
}

func TestResolveCreds_UsernameUnset_RefusesByDefault(t *testing.T) {
	t.Setenv("WEBVIEWER_USERNAME", "")
	t.Setenv("WEBVIEWER_PASSWORD", "some-password")
	t.Setenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS", "")

	_, _, err := resolveCreds()
	if !errors.Is(err, ErrInsecureDefaultCreds) {
		t.Fatalf("want ErrInsecureDefaultCreds, got %v", err)
	}
}

func TestResolveCreds_PasswordUnset_RefusesByDefault(t *testing.T) {
	t.Setenv("WEBVIEWER_USERNAME", "admin")
	t.Setenv("WEBVIEWER_PASSWORD", "")
	t.Setenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS", "")

	_, _, err := resolveCreds()
	if !errors.Is(err, ErrInsecureDefaultCreds) {
		t.Fatalf("want ErrInsecureDefaultCreds, got %v", err)
	}
}

func TestResolveCreds_BothUnset_RefusesByDefault(t *testing.T) {
	t.Setenv("WEBVIEWER_USERNAME", "")
	t.Setenv("WEBVIEWER_PASSWORD", "")
	t.Setenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS", "")

	_, _, err := resolveCreds()
	if !errors.Is(err, ErrInsecureDefaultCreds) {
		t.Fatalf("want ErrInsecureDefaultCreds, got %v", err)
	}
}

func TestResolveCreds_ExplicitOptInReturnsInsecureDefaults(t *testing.T) {
	cases := []string{"1", "true", "True", "TRUE", "yes", "YES"}
	for _, v := range cases {
		t.Run("allow="+v, func(t *testing.T) {
			t.Setenv("WEBVIEWER_USERNAME", "")
			t.Setenv("WEBVIEWER_PASSWORD", "")
			t.Setenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS", v)

			user, pass, err := resolveCreds()
			if err != nil {
				t.Fatalf("want no error with allow=%q, got %v", v, err)
			}
			if user != "admin" || pass != "password" {
				t.Fatalf("want admin/password opt-in defaults, got %q/%q", user, pass)
			}
		})
	}
}

func TestResolveCreds_OptInDoesNotOverrideExplicitEnv(t *testing.T) {
	// If the operator has set real credentials AND set the opt-in flag
	// (e.g. leftover from a previous shell), the real credentials win —
	// we must never silently downgrade to admin/password.
	t.Setenv("WEBVIEWER_USERNAME", "ops")
	t.Setenv("WEBVIEWER_PASSWORD", "S3cret!Production#Password")
	t.Setenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS", "1")

	user, pass, err := resolveCreds()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "ops" || pass != "S3cret!Production#Password" {
		t.Fatalf("opt-in flag must not override real env creds; got %q/%q", user, pass)
	}
}

func TestResolveCreds_OptInFalseyStringsStillRefuse(t *testing.T) {
	// "0", "false", "no", "" must NOT trigger the dev-mode opt-in.
	cases := []string{"0", "false", "no", "nope", "disabled"}
	for _, v := range cases {
		t.Run("allow="+v, func(t *testing.T) {
			t.Setenv("WEBVIEWER_USERNAME", "")
			t.Setenv("WEBVIEWER_PASSWORD", "")
			t.Setenv("QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS", v)

			_, _, err := resolveCreds()
			if !errors.Is(err, ErrInsecureDefaultCreds) {
				t.Fatalf("want ErrInsecureDefaultCreds with allow=%q, got %v", v, err)
			}
		})
	}
}
