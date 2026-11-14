package config

import "testing"

// /api/admin/* requires the JWT role "admin" unless an operator turns the
// check off explicitly: account registration always assigns the role "user",
// so a node that left it off by default let any account read the admin plane.
func TestAdminAPIRequireRoleIsOnByDefault(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{"", true},
		{"true", true},
		{"1", true},
		{"yes", true},
		{"YES", true},
		{"on", true},
		{"anything else", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"No", false},
		{" false ", false},
	} {
		t.Run("env="+tc.env, func(t *testing.T) {
			t.Setenv("QSDM_ADMIN_REQUIRE_ROLE", tc.env)
			cfg := &Config{}
			applyEnvOverrides(cfg)
			if cfg.AdminAPIRequireRole != tc.want {
				t.Fatalf("QSDM_ADMIN_REQUIRE_ROLE=%q: AdminAPIRequireRole = %v, want %v", tc.env, cfg.AdminAPIRequireRole, tc.want)
			}
		})
	}
}

// LoadConfig, the node's loader, ends with the role check on when nothing
// configures it.
func TestLoadConfigAdminRoleRequiredWithoutEnv(t *testing.T) {
	t.Setenv("QSDM_ADMIN_REQUIRE_ROLE", "")
	cfg := &Config{}
	applyEnvOverrides(cfg)
	applyDefaults(cfg)
	if !cfg.AdminAPIRequireRole {
		t.Fatal("the admin role check must be on by default")
	}
}
