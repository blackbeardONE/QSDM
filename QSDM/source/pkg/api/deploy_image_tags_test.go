package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Audit §10a: `:latest` is one label with two meanings across these four
// manifests. `ghcr.io/<owner>/qsdm:latest` is republished on every default-branch
// push (`qsdm-go.yml:489`, `type=raw,value=latest,enable={{is_default_branch}}`),
// while `qsdm-validator:latest` and `qsdm-miner:latest` come only from
// `release-container.yml`, whose `metadata-action` emits `latest` via the
// `latest=auto` default on non-prerelease semver -- and every tag since v0.4.3
// is an `-rc.N` prerelease. So `kubectl apply -f QSDM/deploy/kubernetes/` brings
// up two workloads near main HEAD and two from v0.4.3, under identical-looking
// tags, with nothing in the manifests indicating the difference.
//
// That is worse than a clean failure precisely because it is silent. Choosing
// how to resolve it is a release decision -- run a prerelease in production, or
// split the pinning -- and is deliberately not made here.
//
// What IS made mechanical here is the part that needs no decision: every image a
// manifest references must be one some workflow actually publishes, and the
// registry prefix must be explicit. Both have been real defects. §10a records
// that the tag was once bare `qsdm:latest`, which resolves to Docker Hub and
// does not exist there; adding `ghcr.io/` fixed it and left the audit's
// ImagePullBackOff description stale for two passes.
//
// This test does not close critical #10. It stops #10 from quietly getting
// worse, which is the most a test can do about a release decision.
func TestKubernetesManifestsReferenceOnlyPublishedImages(t *testing.T) {
	root := repoRootForDeployGuard(t)

	manifests := gitLsFiles(t, root, "QSDM/deploy/kubernetes/*.yaml", "QSDM/deploy/kubernetes/*.yml")
	if len(manifests) == 0 {
		t.Fatal("no Kubernetes manifests found -- this guard would pass vacuously")
	}
	workflows := gitLsFiles(t, root, ".github/workflows/*.yml", ".github/workflows/*.yaml")
	if len(workflows) == 0 {
		t.Fatal("no workflows found -- this guard would pass vacuously")
	}

	// Derive the publishable set from EXECUTABLE publish steps only.
	//
	// The first version of this grepped every ghcr.io occurrence in a workflow
	// and skipped any match containing "{". Every real publish ref here is
	// templated -- `images: ghcr.io/${{ github.repository_owner }}/qsdm`
	// (qsdm-go.yml:487) and `echo "ref=ghcr.io/${OWNER_LC}/qsdm-validator"`
	// (release-container.yml:679,824,936) -- so that filter excluded ALL of
	// them, and the entire set came from a prose comment at
	// release-container.yml:14-16 listing the artefact layout.
	//
	// A reviewer showed what that costs both ways: planting a doc comment
	// naming an image nothing builds made the guard ACCEPT a manifest pointing
	// at it, and stripping the comment made every legitimate manifest fail. The
	// guard was reading documentation and calling it CI.
	//
	// So: comments are skipped, and a line only contributes if it is an
	// `images:` input to metadata-action or a `ref=` assignment feeding one.
	// The templated owner segment is discarded rather than used to reject the
	// match, because the owner is not what a manifest has to agree on -- the
	// image name is.
	published := map[string]string{}
	// A templated owner segment contains a space and braces, so it must be
	// collapsed BEFORE matching or the reference is truncated at the template
	// and every real publish step is discarded -- which is how the first version
	// ended up deriving its whole set from a prose comment.
	template := regexp.MustCompile(`\$\{\{[^}]*\}\}|\$\{[^}]*\}`)
	publishLine := regexp.MustCompile(`(?:images:|ref=)\s*"?(ghcr\.io/[^\s"',]+)`)
	for _, rel := range workflows {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if strings.HasPrefix(trimmed, "#") {
				continue // documentation is not a publish step
			}
			m := publishLine.FindStringSubmatch(template.ReplaceAllString(trimmed, "owner"))
			if m == nil {
				continue
			}
			if name := imageRepoName(m[1]); name != "" && !strings.Contains(name, "{") {
				published[name] = rel
			}
		}
	}
	if len(published) == 0 {
		t.Fatal("parsed zero publishable image names from the workflows' publish steps -- the " +
			"guard would pass vacuously against any manifest")
	}

	imageRef := regexp.MustCompile(`(?m)^\s*image:\s*["']?([^\s"']+)`)
	var problems []string
	for _, rel := range manifests {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		for _, m := range imageRef.FindAllStringSubmatch(string(b), -1) {
			ref := m[1]
			if !strings.Contains(ref, "/") {
				problems = append(problems, rel+": "+ref+" has no registry prefix, so it resolves "+
					"to Docker Hub, where these images do not exist (§10a records this exact bug)")
				continue
			}
			name := imageRepoName(ref)
			if _, ok := published[name]; !ok {
				problems = append(problems, rel+": "+ref+" names an image no workflow publishes")
			}
		}
	}

	if len(problems) > 0 {
		t.Fatalf("Kubernetes manifests reference images CI does not publish:\n  %s\n\n"+
			"Every image here must be one some workflow pushes, or `kubectl apply` fails on a "+
			"pull. Publishable names found in the workflows: %v.\n\n"+
			"This guard deliberately does NOT assert which TAG is correct -- audit §10a leaves "+
			"that open, because `:latest` currently means main HEAD for qsdm and v0.4.3 for "+
			"qsdm-validator/qsdm-miner, and resolving that split is a release decision.",
			strings.Join(problems, "\n  "), keysOf(published))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// imageRepoName reduces a reference to its bare repository name.
//
// Splits on "@" before ":", so a digest pin (ghcr.io/owner/qsdm@sha256:...)
// yields "qsdm" and not "qsdm@sha256". The first version did not, which
// rejected digest pinning -- one of the two remediations audit §10a names for
// closing critical #10. A guard that blocks the fix for the thing it guards is
// worse than no guard.
func imageRepoName(ref string) string {
	name := ref[strings.LastIndex(ref, "/")+1:]
	name = strings.SplitN(name, "@", 2)[0]
	return strings.SplitN(name, ":", 2)[0]
}
