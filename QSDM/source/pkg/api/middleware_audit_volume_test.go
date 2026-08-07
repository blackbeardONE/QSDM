package api

import "testing"

func TestIsHighVolumeAuditPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/v1/mining/challenge", want: true},
		{path: "/api/v1/mining/work", want: true},
		{path: "/api/v1/mining/submit", want: true},
		{path: "/api/v1/mining/account", want: false},
		{path: "/api/v1/status", want: false},
		{path: "/api/v1/mining/work/extra", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			if got := isHighVolumeAuditPath(tt.path); got != tt.want {
				t.Fatalf("isHighVolumeAuditPath(%q) = %t, want %t", tt.path, got, tt.want)
			}
		})
	}
}
