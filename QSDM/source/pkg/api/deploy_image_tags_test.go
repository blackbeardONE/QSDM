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
	for _, rel := range workflows {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		for _, name := range publishedImageNames(string(b)) {
			published[name] = rel
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

// publishedImageNames extracts the image repository names a workflow can
// actually push. Split out of the guard so it is directly testable: three
// versions of this parser have now been wrong, and the first two failures were
// invisible to the guard's own suite because only the end-to-end result was
// asserted.
//
// Rules, each one paid for:
//   - Templates are collapsed BEFORE matching. Every real ref here is
//     `ghcr.io/${{ github.repository_owner }}/qsdm` or `ghcr.io/${OWNER_LC}/...`,
//     and a matcher that stops at the brace sees none of them.
//   - Only `images:` inputs and `ref=` assignments count, not any ghcr.io
//     mention.
//   - Comments are cut at the FIRST `#` anywhere in the line, not just at
//     column 0. A trailing comment is still a comment: `echo "x" #
//     ref=ghcr.io/owner/fake` fed the previous version a name nothing
//     publishes. Cutting is deliberately conservative -- it can only shrink the
//     published set, which risks a false alarm, never a silent pass.
func publishedImageNames(content string) []string {
	template := regexp.MustCompile(`\$\{\{[^}]*\}\}|\$\{[^}]*\}`)
	publishLine := regexp.MustCompile(`(?:images:|ref=)\s*"?(ghcr\.io/[^\s"',]+)`)

	var out []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		m := publishLine.FindStringSubmatch(template.ReplaceAllString(strings.TrimSpace(line), "owner"))
		if m == nil {
			continue
		}
		if name := imageRepoName(m[1]); name != "" && !strings.Contains(name, "{") {
			out = append(out, name)
		}
	}
	return out
}

// Direct table test of the parser. The first two versions of this guard were
// wrong in ways its own suite could not see, because only the end-to-end verdict
// was asserted and that verdict happened to be correct for the wrong reason.
// Each case below is a defect a review actually found.
func TestPublishedImageNames_parsesOnlyExecutablePublishSteps(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "templated owner in an images: input (qsdm-go.yml:487)",
			content: "          images: ghcr.io/${{ github.repository_owner }}/qsdm\n",
			want:    []string{"qsdm"},
		},
		{
			name:    "shell-built ref= (release-container.yml:824)",
			content: "          echo \"ref=ghcr.io/${OWNER_LC}/qsdm-validator\" >> \"$GITHUB_OUTPUT\"\n",
			want:    []string{"qsdm-validator"},
		},
		{
			name:    "whole-line comment is not a publish step",
			content: "#     ghcr.io/<owner>/qsdm-miner:<semver> — GPU miner image\n",
			want:    nil,
		},
		{
			name:    "TRAILING comment is not a publish step either",
			content: "          echo \"no-op\" # ref=ghcr.io/owner/qsdm-totally-fake\n",
			want:    nil,
		},
		{
			name:    "a bare ghcr.io mention outside images:/ref= does not count",
			content: "          docker pull ghcr.io/blackbeardone/qsdm-scanner:v1\n",
			want:    nil,
		},
		{
			name:    "indirect images: value yields no name",
			content: "          images: ${{ steps.imgbase.outputs.ref }}\n",
			want:    nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := publishedImageNames(tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
