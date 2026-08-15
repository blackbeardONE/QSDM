package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// initialiseWSAllowedOriginsFromEnv (internal/dashboard/websocket.go) returns
// early when neither QSDM_WS_ALLOWED_ORIGINS nor QSDM_CORS_ALLOWED_ORIGINS is
// set, leaving CheckOrigin permissive -- against that file's own stated
// requirement that production wiring MUST call it.
//
// The bare-metal unit sets it (config/qsdm.service). No container or Kubernetes
// asset did, so the allowlist was absent on exactly the deployments that
// publish a port. Two successive audit passes recorded that as open; both
// grepped the bring-up script and missed that it installs the systemd unit,
// which is why this guard reads the deployed artefacts rather than any script.
//
// Both tests derive their file list from `git ls-files` rather than a literal
// list, so an asset added later cannot sit silently outside the guard -- the
// failure mode the sibling compose healthcheck guard was rewritten to avoid.

func repoRootForDeployGuard(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func gitLsFiles(t *testing.T, root string, patterns ...string) []string {
	t.Helper()
	args := append([]string{"-C", root, "ls-files"}, patterns...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	return strings.Fields(string(out))
}

// A compose service that publishes the dashboard must pin the websocket origin
// allowlist, and pin it to the origin the BROWSER uses -- the host side of the
// published port, not the in-container DASHBOARD_PORT. Those differ on every
// multi-node file here (8081/8082/8084 and 8081/8083/8085 against a container
// port that is always 8081), so an allowlist copied from DASHBOARD_PORT would
// reject two of three dashboards while looking correct in review.
func TestComposeDashboardServicesPinWSOrigins(t *testing.T) {
	root := repoRootForDeployGuard(t)
	files := gitLsFiles(t, root, "*docker-compose*.yml", "*docker-compose*.yaml")
	if len(files) == 0 {
		t.Fatal("git ls-files matched no compose files; the pattern is wrong")
	}

	type service struct {
		Ports       []string  `yaml:"ports"`
		Environment yaml.Node `yaml:"environment"`
	}
	var doc struct {
		Services map[string]service `yaml:"services"`
	}

	checked := 0
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel)) // #nosec G304 -- git-tracked paths
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		doc.Services = nil
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Errorf("%s: parse: %v", rel, err)
			continue
		}
		for name, svc := range doc.Services {
			// Only services that actually publish the dashboard port.
			hostPort := ""
			for _, p := range svc.Ports {
				if !strings.HasSuffix(p, ":8081") {
					continue
				}
				fields := strings.Split(p, ":")
				hostPort = fields[len(fields)-2]
			}
			if hostPort == "" {
				continue
			}
			checked++

			env := envStrings(&svc.Environment)
			origins, ok := env["QSDM_WS_ALLOWED_ORIGINS"]
			if !ok {
				t.Errorf("%s: service %q publishes the dashboard on host port %s but sets no "+
					"QSDM_WS_ALLOWED_ORIGINS, so CheckOrigin stays permissive",
					rel, name, hostPort)
				continue
			}
			if !strings.Contains(origins, ":"+hostPort) {
				t.Errorf("%s: service %q publishes the dashboard on host port %s but its "+
					"allowlist is %q -- the origin the browser sends is the HOST port, so this "+
					"rejects the node's own dashboard", rel, name, hostPort, origins)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no compose service publishing :8081 was found; this guard checked nothing")
	}
	t.Logf("dashboard-publishing compose services checked: %d", checked)
}

// envStrings flattens a compose `environment:` node, which may be a sequence of
// "K=V" strings or a mapping, into a map.
func envStrings(n *yaml.Node) map[string]string {
	out := map[string]string{}
	switch n.Kind {
	case yaml.SequenceNode:
		for _, c := range n.Content {
			k, v, found := strings.Cut(c.Value, "=")
			if found {
				out[k] = v
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = n.Content[i+1].Value
		}
	}
	return out
}

// Every Kubernetes workload running the node image must set the allowlist, and
// the key it references must exist in the ConfigMap. A dangling
// configMapKeyRef does not fail quietly: the pod never starts
// (CreateContainerConfigError), so an unresolvable reference would take the
// node down rather than leave it permissive -- which is why the key's existence
// is asserted rather than assumed.
func TestKubernetesWorkloadsPinWSOrigins(t *testing.T) {
	root := repoRootForDeployGuard(t)
	files := gitLsFiles(t, root, "QSDM/deploy/kubernetes/*.yaml", "QSDM/deploy/kubernetes/*.yml")
	if len(files) == 0 {
		t.Fatal("git ls-files matched no Kubernetes manifests; the pattern is wrong")
	}

	// Collect ConfigMap data first so references can be resolved.
	configMaps := map[string]map[string]string{}
	type manifest struct {
		Kind     string                `yaml:"kind"`
		Metadata struct{ Name string } `yaml:"metadata"`
		Data     map[string]string     `yaml:"data"`
		Spec     struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name string `yaml:"name"`
						Env  []struct {
							Name      string `yaml:"name"`
							Value     string `yaml:"value"`
							ValueFrom struct {
								ConfigMapKeyRef struct {
									Name string `yaml:"name"`
									Key  string `yaml:"key"`
								} `yaml:"configMapKeyRef"`
							} `yaml:"valueFrom"`
						} `yaml:"env"`
					} `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}

	parsed := map[string]manifest{}
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel)) // #nosec G304 -- git-tracked paths
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		var m manifest
		if err := yaml.Unmarshal(raw, &m); err != nil {
			t.Errorf("%s: parse: %v", rel, err)
			continue
		}
		if m.Kind == "ConfigMap" {
			configMaps[m.Metadata.Name] = m.Data
		}
		parsed[rel] = m
	}

	checked := 0
	for rel, m := range parsed {
		if m.Kind != "Deployment" && m.Kind != "StatefulSet" && m.Kind != "DaemonSet" {
			continue
		}
		for _, c := range m.Spec.Template.Spec.Containers {
			// Only workloads that configure the dashboard at all.
			var hasDashboard bool
			var wsEnv *struct {
				Name      string `yaml:"name"`
				Value     string `yaml:"value"`
				ValueFrom struct {
					ConfigMapKeyRef struct {
						Name string `yaml:"name"`
						Key  string `yaml:"key"`
					} `yaml:"configMapKeyRef"`
				} `yaml:"valueFrom"`
			}
			for i := range c.Env {
				switch c.Env[i].Name {
				case "QSDM_DASHBOARD_BIND_ADDRESS":
					hasDashboard = true
				case "QSDM_WS_ALLOWED_ORIGINS":
					wsEnv = &c.Env[i]
				}
			}
			if !hasDashboard {
				continue
			}
			checked++
			if wsEnv == nil {
				t.Errorf("%s: container %q binds the dashboard but sets no "+
					"QSDM_WS_ALLOWED_ORIGINS, so CheckOrigin stays permissive", rel, c.Name)
				continue
			}
			ref := wsEnv.ValueFrom.ConfigMapKeyRef
			if ref.Name == "" {
				if strings.TrimSpace(wsEnv.Value) == "" {
					t.Errorf("%s: container %q sets an empty QSDM_WS_ALLOWED_ORIGINS, which "+
						"leaves CheckOrigin permissive exactly as if it were unset", rel, c.Name)
				}
				continue
			}
			data, ok := configMaps[ref.Name]
			if !ok {
				t.Errorf("%s: container %q references ConfigMap %q, which no tracked manifest "+
					"defines; the pod would fail to start", rel, c.Name, ref.Name)
				continue
			}
			val, ok := data[ref.Key]
			if !ok {
				t.Errorf("%s: container %q references ConfigMap %q key %q, which does not "+
					"exist; the pod would fail to start with CreateContainerConfigError",
					rel, c.Name, ref.Name, ref.Key)
				continue
			}
			if strings.TrimSpace(val) == "" {
				t.Errorf("%s: container %q resolves QSDM_WS_ALLOWED_ORIGINS to an empty value, "+
					"which leaves CheckOrigin permissive exactly as if it were unset", rel, c.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Kubernetes workload configuring the dashboard was found; this guard checked nothing")
	}
	t.Logf("dashboard-configuring Kubernetes containers checked: %d", checked)
}
