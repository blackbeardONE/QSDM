package dashboard

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// The 3D panels shipped broken for several releases: index.html declared
//
//	<script type="importmap" src="/static/importmap.json"></script>
//
// and external import maps are specified but implemented by no shipping
// browser. The src attribute was ignored, so `import ... from 'three'` failed
// with "Failed to resolve module specifier" and both WebGL containers stayed
// empty. Nothing in CI noticed, because the Go handlers were all still healthy.
//
// These tests pin the invariants that make that failure mode impossible:
// three.js is vendored and embedded, no external import map is declared, and
// every module specifier resolves to a file that actually ships.

var moduleSpecifier = regexp.MustCompile(`(?:^|\s)from\s*['"]([^'"]+)['"]`)

func TestVendoredThreeIsEmbedded(t *testing.T) {
	for _, name := range []string{
		"static/vendor/three.module.min.js",
		"static/vendor/OrbitControls.js",
	} {
		data, err := staticFiles.ReadFile(name)
		if err != nil {
			t.Fatalf("%s is not embedded: %v", name, err)
		}
		// three r170's minified module build is ~690 KB and OrbitControls
		// ~32 KB; anything tiny means a truncated or placeholder download.
		if len(data) < 8*1024 {
			t.Errorf("%s is only %d bytes, expected a real vendored build", name, len(data))
		}
	}
}

func TestIndexDeclaresNoExternalImportMap(t *testing.T) {
	html := readStatic(t, "static/index.html")

	if strings.Contains(html, `type="importmap"`) {
		t.Error(`index.html declares <script type="importmap">: an external ` +
			`import map (src=...) is not implemented in any browser, and an ` +
			`inline one is blocked by the script-src 'self' CSP. Import ` +
			`three.js by path from /static/vendor instead.`)
	}
	if strings.Contains(html, "importmap.json") {
		t.Error("index.html still references importmap.json, which was removed")
	}
	if !strings.Contains(html, `src="/static/viz3d.js"`) {
		t.Error("index.html no longer loads /static/viz3d.js")
	}
}

func TestStaticModuleSpecifiersResolve(t *testing.T) {
	entries, err := fs.Glob(staticFiles, "static/*.js")
	if err != nil {
		t.Fatalf("glob static/*.js: %v", err)
	}
	vendor, err := fs.Glob(staticFiles, "static/vendor/*.js")
	if err != nil {
		t.Fatalf("glob static/vendor/*.js: %v", err)
	}
	entries = append(entries, vendor...)
	if len(entries) == 0 {
		t.Fatal("no embedded JS files found")
	}

	checked := 0
	for _, file := range entries {
		source := readStatic(t, file)
		for _, match := range moduleSpecifier.FindAllStringSubmatch(source, -1) {
			spec := match[1]
			// Bare specifiers such as "three" only resolve through an import
			// map, which this dashboard deliberately does not use.
			if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") && !strings.HasPrefix(spec, "/") {
				t.Errorf("%s imports bare specifier %q; use a path under /static/vendor instead", file, spec)
				continue
			}

			var target string
			if strings.HasPrefix(spec, "/") {
				target = "static" + spec
			} else {
				target = path.Join(path.Dir(file), spec)
			}
			if _, err := staticFiles.ReadFile(target); err != nil {
				t.Errorf("%s imports %q which resolves to %s, not embedded: %v", file, spec, target, err)
			}
			checked++
		}
	}

	if checked == 0 {
		t.Error("no module specifiers were checked; the regex or the layout changed")
	}
}

func readStatic(t *testing.T, name string) string {
	t.Helper()
	data, err := staticFiles.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}
