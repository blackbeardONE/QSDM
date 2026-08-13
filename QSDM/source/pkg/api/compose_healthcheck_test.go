package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Six consecutive commits shipped a broken container healthcheck, each for a
// different reason: an authenticated route, a tool absent from the image, a
// method the handler rejects, and finally a URL with no handler at all. Each
// fix was verified in isolation and each missed a sibling file.
//
// The common cause is that nothing ever compared what the probes request
// against what the API actually serves. This test does, so the next mistake in
// that family fails here rather than in a deployment.
func TestComposeHealthchecksProbeAServedRoute(t *testing.T) {
	// pkg/api -> source -> QSDM
	root := filepath.Join("..", "..", "..")
	files := []string{
		"docker-compose.yml",
		"docker-compose.production.yml",
		filepath.Join("deploy", "docker-compose.single.yml"),
		filepath.Join("deploy", "docker-compose.cluster.yml"),
	}

	// Routes the API serves without authentication, i.e. the only ones a
	// container healthcheck can legitimately use. Kept explicit rather than
	// derived, so widening it is a visible decision.
	publicProbeRoutes := map[string]bool{
		"/api/v1/health":       true,
		"/api/v1/health/live":  true,
		"/api/v1/health/ready": true,
	}

	urlRe := regexp.MustCompile(`https?://[^"'\s\]]+`)
	pathRe := regexp.MustCompile(`^https?://[^/]+(/[^?#]*)?`)

	checked := 0
	for _, rel := range files {
		path := filepath.Join(root, rel)
		raw, err := os.ReadFile(path) // #nosec G304 -- fixed in-repo paths
		if err != nil {
			if os.IsNotExist(err) {
				t.Logf("%s not present, skipping", rel)
				continue
			}
			t.Fatalf("read %s: %v", rel, err)
		}

		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "test:") || !strings.Contains(line, "healthcheck") &&
				!strings.Contains(line, "wget") && !strings.Contains(line, "curl") {
				continue
			}
			for _, u := range urlRe.FindAllString(line, -1) {
				m := pathRe.FindStringSubmatch(u)
				if m == nil {
					continue
				}
				route := m[1]
				if route == "" {
					route = "/"
				}
				checked++
				if !publicProbeRoutes[route] {
					t.Errorf("%s:%d probes %q, which is not a public API route. "+
						"A healthcheck must target a route the API serves without "+
						"authentication; %q is either unregistered or behind auth.",
						rel, i+1, route, route)
				}
			}

			// HEAD-issuing probes fail against HealthLive, which 405s on
			// non-GET. See TestHealthLive_MethodsThatProbesUse.
			if strings.Contains(line, "--spider") || strings.Contains(line, `"-I"`) ||
				strings.Contains(line, "--head") {
				t.Errorf("%s:%d issues a HEAD request; the health handlers accept GET only", rel, i+1)
			}
			// curl is not installed in the runtime image (QSDM/Dockerfile
			// installs wget only), so a curl probe cannot execute.
			if strings.Contains(line, `"curl"`) {
				t.Errorf("%s:%d uses curl, which is not present in the runtime image", rel, i+1)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no healthcheck URLs were examined; the parser or the file list is wrong")
	}
	t.Logf("checked %d healthcheck URL(s)", checked)
}
