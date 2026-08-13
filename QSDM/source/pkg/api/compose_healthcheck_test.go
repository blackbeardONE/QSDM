package api

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Six consecutive commits shipped a broken container healthcheck, each for a
// different reason: an authenticated route, a tool absent from the image, a
// method the handler rejects, and a URL with no handler at all. Each fix was
// verified in isolation and each missed a sibling.
//
// The first version of this guard scanned the compose files as TEXT, and review
// defeated it three ways: a CMD-SHELL probe whose `curl` was unquoted, a
// `wget --method=HEAD`, and -- worst -- a block scalar (`test: |` with the
// command on the following line), which the line filter dropped silently while
// still reporting a pass. A guard that validates one rendering of the YAML
// rather than its meaning is the same defect it exists to catch.
//
// So this parses the YAML and inspects the healthcheck command as tokens.

type composeFile struct {
	Services map[string]struct {
		Healthcheck struct {
			Test yaml.Node `yaml:"test"`
		} `yaml:"healthcheck"`
	} `yaml:"services"`
}

// probeTokens flattens a compose `test:` value into argv-ish tokens. It accepts
// every form compose allows: a sequence (["CMD", ...] / ["CMD-SHELL", ...]) and
// a string, including block scalars, which are shell and so are split on
// whitespace.
func probeTokens(n *yaml.Node) []string {
	switch n.Kind {
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			out = append(out, probeTokens(c)...)
		}
		return out
	case yaml.ScalarNode:
		return strings.Fields(n.Value)
	}
	return nil
}

func TestComposeHealthchecksProbeAServedRoute(t *testing.T) {
	// The REPOSITORY root, not QSDM/: a fifth tracked compose file lives under
	// apps/ and was invisible to a search rooted at QSDM/, which is how the
	// first version of this guard came to cover four files and miss it.
	topOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root := strings.TrimSpace(string(topOut))

	// Derived from git, not hardcoded, so a compose file added later cannot be
	// silently outside the guard.
	out, err := exec.Command("git", "-C", root, "ls-files", "*docker-compose*.yml", "*docker-compose*.yaml").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	var files []string
	for _, f := range strings.Fields(string(out)) {
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("git ls-files matched no compose files; the pattern is wrong")
	}
	t.Logf("compose files discovered: %v", files)

	// The only routes a container healthcheck may use: registered AND public.
	// Explicit so that widening it is a visible decision in a diff.
	publicProbeRoutes := map[string]bool{
		"/api/v1/health": true, "/api/v1/health/live": true, "/api/v1/health/ready": true,
	}
	urlRe := regexp.MustCompile(`https?://[^\s"']+`)
	pathRe := regexp.MustCompile(`^https?://[^/]+(/[^?#]*)?`)
	// Word-boundary, so an unquoted curl inside a CMD-SHELL string is caught.
	curlRe := regexp.MustCompile(`(^|[\s/;&|])curl($|[\s;&|])`)

	probes := 0
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel)) // #nosec G304 -- git-tracked paths
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		var cf composeFile
		if err := yaml.Unmarshal(raw, &cf); err != nil {
			t.Errorf("%s: parse: %v", rel, err)
			continue
		}
		for name, svc := range cf.Services {
			toks := probeTokens(&svc.Healthcheck.Test)
			if len(toks) == 0 {
				continue
			}
			probes++
			joined := strings.Join(toks, " ")
			where := rel + " service " + name

			if curlRe.MatchString(" " + joined + " ") {
				t.Errorf("%s: probes with curl, which QSDM/Dockerfile does not install (it installs wget only)", where)
			}
			// Every way of asking wget for a HEAD. HealthLive 405s on non-GET.
			for _, head := range []string{"--spider", "--method=HEAD", "-I", "--head", "--request=HEAD"} {
				if strings.Contains(joined, head) {
					t.Errorf("%s: probe uses %s, which issues HEAD; the health handlers accept GET only", where, head)
				}
			}
			urls := urlRe.FindAllString(joined, -1)
			if len(urls) == 0 {
				t.Errorf("%s: healthcheck has no URL: %q", where, joined)
				continue
			}
			for _, u := range urls {
				m := pathRe.FindStringSubmatch(u)
				route := "/"
				if m != nil && m[1] != "" {
					route = m[1]
				}
				if !publicProbeRoutes[route] {
					t.Errorf("%s: probes %q, which is not a registered public API route; "+
						"a healthcheck must target a route served without authentication", where, route)
				}
			}
		}
	}

	if probes == 0 {
		t.Fatal("no healthchecks were examined; the parser or the file discovery is broken")
	}
	t.Logf("checked %d healthcheck(s)", probes)
}

// Kubernetes probes are the sibling mechanism the compose guard does not cover.
// They use native httpGet rather than a shell command, so the tool and method
// classes cannot arise -- kubelet always issues a GET -- but the ROUTE class
// can, and that is the one that caused two of the six incidents. Review flagged
// these manifests as currently correct with zero test coverage, which is
// precisely the state each of the six defects was in beforehand.

type k8sHTTPGet struct {
	Path   string `yaml:"path"`
	Port   string `yaml:"port"`
	Scheme string `yaml:"scheme"`
}

type k8sProbe struct {
	HTTPGet *k8sHTTPGet `yaml:"httpGet"`
}

type k8sContainer struct {
	Name           string    `yaml:"name"`
	LivenessProbe  *k8sProbe `yaml:"livenessProbe"`
	ReadinessProbe *k8sProbe `yaml:"readinessProbe"`
	StartupProbe   *k8sProbe `yaml:"startupProbe"`
}

type k8sManifest struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []k8sContainer `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func TestKubernetesProbesTargetAServedRoute(t *testing.T) {
	topOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root := strings.TrimSpace(string(topOut))

	out, err := exec.Command("git", "-C", root, "ls-files", "*kubernetes/*.yaml", "*kubernetes/*.yml").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	files := strings.Fields(string(out))
	if len(files) == 0 {
		t.Skip("no kubernetes manifests tracked")
	}

	publicProbeRoutes := map[string]bool{
		"/api/v1/health": true, "/api/v1/health/live": true, "/api/v1/health/ready": true,
	}

	probes := 0
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel)) // #nosec G304 -- git-tracked paths
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		for {
			var m k8sManifest
			// io.EOF ends the stream. Any OTHER error is a malformed document,
			// and breaking on it would stop scanning the rest of the file while
			// still reporting a pass -- the same silent-truncation defect this
			// guard exists to catch, which is exactly how review found it here.
			if err := dec.Decode(&m); err != nil {
				if !errors.Is(err, io.EOF) {
					t.Errorf("%s: malformed YAML document, remaining documents unchecked: %v", rel, err)
				}
				break
			}
			for _, c := range m.Spec.Template.Spec.Containers {
				for kind, pr := range map[string]*k8sProbe{
					"livenessProbe": c.LivenessProbe, "readinessProbe": c.ReadinessProbe,
					"startupProbe": c.StartupProbe,
				} {
					if pr == nil || pr.HTTPGet == nil {
						continue
					}
					probes++
					if !publicProbeRoutes[pr.HTTPGet.Path] {
						t.Errorf("%s: container %q %s probes %q, which is not a registered public API route",
							rel, c.Name, kind, pr.HTTPGet.Path)
					}
					if sc := pr.HTTPGet.Scheme; sc != "" && !strings.EqualFold(sc, "HTTP") {
						t.Errorf("%s: container %q %s uses scheme %q; the API serves plaintext HTTP by default",
							rel, c.Name, kind, sc)
					}
				}
			}
		}
	}
	if probes == 0 {
		t.Skip("no httpGet probes found in tracked manifests")
	}
	t.Logf("checked %d kubernetes probe(s)", probes)
}

// The truncation defect was verified by hand and the artefact thrown away, so
// nothing stopped it coming back. This pins it with an in-memory fixture: a
// valid document, then a malformed one, then a document carrying a bad probe
// route. Decoding must report the malformed document rather than treating it
// as end-of-stream and silently skipping everything after it.
func TestKubernetesDecode_MalformedDocumentIsReportedNotSkipped(t *testing.T) {
	const stream = `kind: ConfigMap
metadata:
  name: fine
---
kind: ConfigMap
data:
  broken: [unclosed
---
kind: Deployment
spec:
  template:
    spec:
      containers:
      - name: late
        livenessProbe:
          httpGet:
            path: /not/public
`
	dec := yaml.NewDecoder(strings.NewReader(stream))
	var docs int
	var decodeErr error
	for {
		var m k8sManifest
		if err := dec.Decode(&m); err != nil {
			if !errors.Is(err, io.EOF) {
				decodeErr = err
			}
			break
		}
		docs++
	}

	if decodeErr == nil {
		t.Fatal("a malformed document must surface as a non-EOF error; if this " +
			"passes, the decoder swallowed it and later documents would be " +
			"skipped in silence")
	}
	if errors.Is(decodeErr, io.EOF) {
		t.Error("the malformed document was reported as EOF")
	}
	// The point of the fixture: the bad probe lives AFTER the malformed
	// document, so a scan that stops at the error never sees it.
	if docs >= 3 {
		t.Errorf("expected the stream to stop before the third document, decoded %d", docs)
	}
	if !strings.Contains(stream[strings.Index(stream, "kind: Deployment"):], "/not/public") {
		t.Fatal("fixture no longer contains the trailing bad route it exists to represent")
	}
}
