package api

// Drift check between the routes this package registers and the published
// API description, QSDM/docs/docs/openapi.yaml.
//
// The registered routes are read from this package's source (every
// HandleFunc / Handle call, string constants resolved) and confirmed against
// the real mux built by Server.registerRoutes, so no running node is needed.
// The test fails when:
//
//   - a registered route is neither described in openapi.yaml nor listed in
//     undocumentedRoutes (with a reason);
//   - openapi.yaml lists a path that no registered route serves;
//   - an operation's markers disagree with the middleware that enforces them:
//     x-qsdm-access (public | authenticated | operator | closed) and the
//     bearerAuth security requirement against isPublicEndpoint, and
//     x-qsdm-follower: refused against followerReadOnlyAllows;
//   - a documented method is answered with 405 Method Not Allowed;
//   - the file breaks basic OpenAPI 3.0 structure (operationIds, responses,
//     path parameters, local $refs, declared tags).

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

// openAPISpecFile is openapi.yaml relative to this package directory.
var openAPISpecFile = filepath.FromSlash("../../../docs/docs/openapi.yaml")

// adminRouteReason is why the admin API is left out of openapi.yaml.
const adminRouteReason = "operator-only admin API (AdminAPI.RegisterRoutes, mounted only when the node " +
	"calls SetAdminAPI): needs a dashboard JWT and, when configured, role=admin and a verified mTLS " +
	"client certificate (AdminAccessMiddleware); not part of the public /api/v1 description"

// undocumentedRoutes lists registered route patterns that openapi.yaml
// deliberately does not describe, each with the reason. A registered route
// that is in neither openapi.yaml nor this list fails the test, so adding a
// route forces a decision. Keep entries exact: one per registered pattern.
var undocumentedRoutes = map[string]string{
	"/api/admin/accounts":                         adminRouteReason,
	"/api/admin/account/":                         adminRouteReason,
	"/api/admin/validators":                       adminRouteReason,
	"/api/admin/finality":                         adminRouteReason,
	"/api/admin/mempool":                          adminRouteReason,
	"/api/admin/receipts":                         adminRouteReason,
	"/api/admin/receipts/stats":                   adminRouteReason,
	"/api/admin/peers":                            adminRouteReason,
	"/api/admin/peers/banned":                     adminRouteReason,
	"/api/admin/traces":                           adminRouteReason,
	"/api/admin/traces/stats":                     adminRouteReason,
	"/api/admin/chain":                            adminRouteReason,
	"/api/admin/consensus/bft/follower":           adminRouteReason,
	"/api/admin/consensus/pol/summary":            adminRouteReason,
	"/api/admin/consensus/pol/prevote-lock/":      adminRouteReason,
	"/api/admin/consensus/pol/round-certificate/": adminRouteReason,
	"/api/admin/overview":                         adminRouteReason,
	"/api/admin/audit":                            adminRouteReason,
	"/api/admin/config/reload-dry-run":            adminRouteReason,
}

// openAPIAccessValues are the allowed values of the x-qsdm-access extension.
var openAPIAccessValues = map[string]string{
	"public":        "no JWT; anyone may call it (writes carry their own signature)",
	"authenticated": "AuthMiddleware requires a Bearer JWT",
	"operator":      "no JWT, but the handler admits only the node operator",
	"closed":        "registered but permanently disabled",
}

var openAPIMethods = map[string]string{
	"get":     http.MethodGet,
	"put":     http.MethodPut,
	"post":    http.MethodPost,
	"delete":  http.MethodDelete,
	"options": http.MethodOptions,
	"head":    http.MethodHead,
	"patch":   http.MethodPatch,
	"trace":   http.MethodTrace,
}

// openAPIPathItemFields are the non-operation keys a path item may carry.
var openAPIPathItemFields = map[string]bool{
	"summary":     true,
	"description": true,
	"parameters":  true,
	"servers":     true,
}

var openAPIPathTemplateVar = regexp.MustCompile(`\{([^{}/]+)\}`)

type openAPISpec struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Security *[]map[string][]string `yaml:"security"`
	Tags     []struct {
		Name string `yaml:"name"`
	} `yaml:"tags"`
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Parameters map[string]openAPIParameter `yaml:"parameters"`
	} `yaml:"components"`
}

type openAPIParameter struct {
	Ref      string `yaml:"$ref"`
	Name     string `yaml:"name"`
	In       string `yaml:"in"`
	Required bool   `yaml:"required"`
}

type openAPIOperation struct {
	OperationID string                 `yaml:"operationId"`
	Summary     string                 `yaml:"summary"`
	Tags        []string               `yaml:"tags"`
	Access      string                 `yaml:"x-qsdm-access"`
	Follower    string                 `yaml:"x-qsdm-follower"`
	Security    *[]map[string][]string `yaml:"security"`
	Parameters  []openAPIParameter     `yaml:"parameters"`
	Responses   map[string]yaml.Node   `yaml:"responses"`
}

// documentedOperation is one method of one openapi.yaml path.
type documentedOperation struct {
	method   string // GET, POST, ...
	specPath string // path key in openapi.yaml, e.g. /tasks/{task_id}
	urlPath  string // request path with sample values, e.g. /api/v1/tasks/sample
	op       openAPIOperation
	params   []openAPIParameter // path-item and operation parameters, $refs resolved
}

func (d documentedOperation) String() string { return d.method + " " + d.specPath }

// sourceRoute is a route pattern registered in this package's source.
type sourceRoute struct {
	pattern string
	where   string // file:line of the registration
}

func TestOpenAPIMatchesRegisteredRoutes(t *testing.T) {
	spec, root := loadOpenAPISpec(t)
	ops := documentedOperations(t, spec)
	routes := sourceRoutes(t)
	mux := newDriftCheckMux(t)

	registered := make(map[string]sourceRoute, len(routes))
	for _, route := range routes {
		if previous, dup := registered[route.pattern]; dup {
			t.Errorf("route %s is registered twice (%s and %s)", route.pattern, previous.where, route.where)
		}
		registered[route.pattern] = route
	}

	t.Run("source routes are the routes registerRoutes mounts", func(t *testing.T) {
		for _, route := range routes {
			if got := servingPattern(mux, http.MethodGet, route.pattern); got != route.pattern {
				t.Errorf("%s registers %s, but the mux built by registerRoutes serves it with %q; "+
					"conditional or indirect registration needs teaching to this test", route.where, route.pattern, got)
			}
		}
	})

	t.Run("openapi.yaml is well formed", func(t *testing.T) {
		checkOpenAPIStructure(t, spec, root, ops)
	})

	t.Run("every documented path is a registered route", func(t *testing.T) {
		for _, d := range ops {
			pattern := servingPattern(mux, d.method, d.urlPath)
			switch {
			case pattern == "":
				t.Errorf("openapi.yaml documents %s (%s), but no registered route serves it", d, d.urlPath)
			case undocumentedRoutes[pattern] != "":
				t.Errorf("openapi.yaml documents %s, served by %s, which undocumentedRoutes says is not described; "+
					"remove one of the two", d, pattern)
			}
		}
	})

	t.Run("every registered route is documented or allowlisted", func(t *testing.T) {
		covered := map[string]bool{}
		for _, d := range ops {
			covered[servingPattern(mux, d.method, d.urlPath)] = true
		}
		for _, route := range routes {
			if covered[route.pattern] || undocumentedRoutes[route.pattern] != "" {
				continue
			}
			t.Errorf("route %s (registered at %s) is missing from openapi.yaml: describe it there, "+
				"or list it in undocumentedRoutes with the reason it stays undocumented", route.pattern, route.where)
		}
		for pattern, reason := range undocumentedRoutes {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("undocumentedRoutes[%q] has no reason", pattern)
			}
			if _, ok := registered[pattern]; !ok {
				t.Errorf("undocumentedRoutes lists %s, which is not registered any more; remove the entry", pattern)
			}
		}
	})

	t.Run("access markers match the auth middleware", func(t *testing.T) {
		globalBearer := spec.Security != nil && hasBearerRequirement(*spec.Security)
		for _, d := range ops {
			if _, ok := openAPIAccessValues[d.op.Access]; !ok {
				t.Errorf("%s: x-qsdm-access is %q; want one of %s", d, d.op.Access, sortedKeys(openAPIAccessValues))
				continue
			}
			needsJWT := !isPublicEndpoint(d.urlPath)
			bearer := globalBearer
			if d.op.Security != nil {
				bearer = hasBearerRequirement(*d.op.Security)
			}
			if bearer != needsJWT {
				t.Errorf("%s: bearerAuth security requirement is %v, but AuthMiddleware requires a JWT for %s: %v "+
					"(isPublicEndpoint in middleware.go)", d, bearer, d.urlPath, needsJWT)
			}
			if d.op.Access == "public" && needsJWT {
				t.Errorf("%s: x-qsdm-access is public, but isPublicEndpoint(%q) is false", d, d.urlPath)
			}
			if d.op.Access == "authenticated" && !needsJWT {
				t.Errorf("%s: x-qsdm-access is authenticated, but isPublicEndpoint(%q) is true", d, d.urlPath)
			}
		}
	})

	t.Run("follower markers match the follower read-only policy", func(t *testing.T) {
		for _, d := range ops {
			if d.op.Follower != "" && d.op.Follower != "refused" {
				t.Errorf("%s: x-qsdm-follower is %q; the only value is \"refused\"", d, d.op.Follower)
				continue
			}
			refused := !followerReadOnlyAllows(d.method, d.urlPath)
			if (d.op.Follower == "refused") != refused {
				t.Errorf("%s: x-qsdm-follower: refused is %v, but a read-only follower refuses it: %v "+
					"(followerReadOnlyAllows in follower_readonly.go)", d, d.op.Follower == "refused", refused)
			}
		}
	})

	t.Run("documented methods are accepted", func(t *testing.T) {
		for _, d := range ops {
			if code, ok := probeStatus(mux, d.method, d.urlPath); ok && code == http.StatusMethodNotAllowed {
				t.Errorf("openapi.yaml documents %s, but the handler answers %s with 405 Method Not Allowed",
					d, d.method)
			}
		}
	})
}

// loadOpenAPISpec reads openapi.yaml both as a node tree (for $ref checks)
// and as the typed view the drift checks use.
func loadOpenAPISpec(t *testing.T) (openAPISpec, *yaml.Node) {
	t.Helper()
	raw, err := os.ReadFile(openAPISpecFile)
	if err != nil {
		t.Fatalf("read %s: %v", openAPISpecFile, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse %s: %v", openAPISpecFile, err)
	}
	var spec openAPISpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode %s: %v", openAPISpecFile, err)
	}
	if len(spec.Paths) == 0 {
		t.Fatalf("%s has no paths", openAPISpecFile)
	}
	return spec, &root
}

// openAPIBasePath returns the path of the server URLs (all must agree).
func openAPIBasePath(t *testing.T, spec openAPISpec) string {
	t.Helper()
	if len(spec.Servers) == 0 {
		t.Fatal("openapi.yaml declares no servers, so its paths have no base path")
	}
	base := ""
	for i, server := range spec.Servers {
		u, err := url.Parse(server.URL)
		if err != nil {
			t.Fatalf("servers[%d].url %q: %v", i, server.URL, err)
		}
		p := strings.TrimSuffix(u.Path, "/")
		if i == 0 {
			base = p
		} else if p != base {
			t.Fatalf("servers disagree on the base path: %q and %q", base, p)
		}
	}
	return base
}

func documentedOperations(t *testing.T, spec openAPISpec) []documentedOperation {
	t.Helper()
	base := openAPIBasePath(t, spec)
	var ops []documentedOperation
	for specPath, item := range spec.Paths {
		if !strings.HasPrefix(specPath, "/") || strings.ContainsAny(specPath, "?#") {
			t.Errorf("openapi.yaml path %q must start with / and carry no query or fragment", specPath)
			continue
		}
		var shared []openAPIParameter
		if node, ok := item["parameters"]; ok {
			if err := node.Decode(&shared); err != nil {
				t.Errorf("%s: parameters: %v", specPath, err)
			}
		}
		urlPath := base + openAPIPathTemplateVar.ReplaceAllString(specPath, "sample")
		for key, node := range item {
			method, isMethod := openAPIMethods[key]
			if !isMethod {
				if !openAPIPathItemFields[key] {
					t.Errorf("%s: unexpected path item key %q", specPath, key)
				}
				continue
			}
			var op openAPIOperation
			if err := node.Decode(&op); err != nil {
				t.Errorf("%s %s: %v", strings.ToUpper(key), specPath, err)
				continue
			}
			params := make([]openAPIParameter, 0, len(shared)+len(op.Parameters))
			for _, p := range append(append([]openAPIParameter{}, shared...), op.Parameters...) {
				params = append(params, resolveOpenAPIParameter(t, spec, specPath, p))
			}
			ops = append(ops, documentedOperation{method: method, specPath: specPath, urlPath: urlPath, op: op, params: params})
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].specPath != ops[j].specPath {
			return ops[i].specPath < ops[j].specPath
		}
		return ops[i].method < ops[j].method
	})
	return ops
}

func resolveOpenAPIParameter(t *testing.T, spec openAPISpec, specPath string, p openAPIParameter) openAPIParameter {
	t.Helper()
	if p.Ref == "" {
		return p
	}
	const prefix = "#/components/parameters/"
	if !strings.HasPrefix(p.Ref, prefix) {
		t.Errorf("%s: parameter $ref %q must point into %s", specPath, p.Ref, prefix)
		return p
	}
	resolved, ok := spec.Components.Parameters[strings.TrimPrefix(p.Ref, prefix)]
	if !ok {
		t.Errorf("%s: parameter $ref %q does not resolve", specPath, p.Ref)
		return p
	}
	return resolved
}

func checkOpenAPIStructure(t *testing.T, spec openAPISpec, root *yaml.Node, ops []documentedOperation) {
	t.Helper()
	if !strings.HasPrefix(spec.OpenAPI, "3.0.") {
		t.Errorf("openapi is %q; this file is written as OpenAPI 3.0.x", spec.OpenAPI)
	}
	if spec.Info.Title == "" || spec.Info.Version == "" {
		t.Error("info.title and info.version are required")
	}
	declaredTags := map[string]bool{}
	for _, tag := range spec.Tags {
		declaredTags[tag.Name] = true
	}
	operationIDs := map[string]string{}
	for _, d := range ops {
		if d.op.OperationID == "" {
			t.Errorf("%s has no operationId", d)
		} else if previous, dup := operationIDs[d.op.OperationID]; dup {
			t.Errorf("operationId %q is used by both %s and %s", d.op.OperationID, previous, d)
		} else {
			operationIDs[d.op.OperationID] = d.String()
		}
		if d.op.Summary == "" {
			t.Errorf("%s has no summary", d)
		}
		if len(d.op.Responses) == 0 {
			t.Errorf("%s has no responses", d)
		}
		if len(d.op.Tags) == 0 {
			t.Errorf("%s has no tag", d)
		}
		for _, tag := range d.op.Tags {
			if !declaredTags[tag] {
				t.Errorf("%s uses tag %q, which the top-level tags list does not declare", d, tag)
			}
		}
		checkPathParameters(t, d)
	}
	for _, problem := range unresolvedLocalRefs(root) {
		t.Error(problem)
	}
}

// checkPathParameters requires every {var} in the path to be declared as a
// required path parameter, and every path parameter to appear in the path.
func checkPathParameters(t *testing.T, d documentedOperation) {
	t.Helper()
	inTemplate := map[string]bool{}
	for _, m := range openAPIPathTemplateVar.FindAllStringSubmatch(d.specPath, -1) {
		inTemplate[m[1]] = true
	}
	declared := map[string]bool{}
	seen := map[string]bool{}
	for _, p := range d.params {
		key := p.In + ":" + p.Name
		if seen[key] {
			t.Errorf("%s declares the %s parameter %q twice", d, p.In, p.Name)
		}
		seen[key] = true
		if p.In != "path" {
			continue
		}
		declared[p.Name] = true
		if !inTemplate[p.Name] {
			t.Errorf("%s declares path parameter %q, which is not in the path", d, p.Name)
		}
		if !p.Required {
			t.Errorf("%s: path parameter %q must be required", d, p.Name)
		}
	}
	for name := range inTemplate {
		if !declared[name] {
			t.Errorf("%s: path variable {%s} is not declared as a path parameter", d, name)
		}
	}
}

// unresolvedLocalRefs walks the document and reports every $ref that is not
// a local reference to an existing node.
func unresolvedLocalRefs(root *yaml.Node) []string {
	var problems []string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, value := n.Content[i], n.Content[i+1]
				if key.Value == "$ref" && value.Kind == yaml.ScalarNode {
					if !strings.HasPrefix(value.Value, "#/") {
						problems = append(problems, fmt.Sprintf("line %d: $ref %q is not a local reference", value.Line, value.Value))
					} else if lookupYAMLPointer(root, value.Value) == nil {
						problems = append(problems, fmt.Sprintf("line %d: $ref %q does not resolve", value.Line, value.Value))
					}
				}
			}
		}
		for _, child := range n.Content {
			walk(child)
		}
	}
	walk(root)
	return problems
}

func lookupYAMLPointer(root *yaml.Node, ref string) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == part {
				next = n.Content[i+1]
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

func hasBearerRequirement(requirements []map[string][]string) bool {
	for _, requirement := range requirements {
		if _, ok := requirement["bearerAuth"]; ok {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// sourceRoutes returns every pattern passed to a HandleFunc or Handle call in
// this package's non-test Go files (for the current build). A pattern that is
// neither a string literal nor a string constant of this package fails the
// test, so a new way of registering routes cannot slip past the check.
func sourceRoutes(t *testing.T) []sourceRoute {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if match, err := build.Default.MatchFile(".", name); err != nil || !match {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}

	constants := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if value, err := strconv.Unquote(lit.Value); err == nil {
							constants[name.Name] = value
						}
					}
				}
			}
		}
	}

	var routes []sourceRoute
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") || len(call.Args) != 2 {
				return true
			}
			pos := fset.Position(call.Pos())
			where := fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
			pattern := ""
			switch arg := call.Args[0].(type) {
			case *ast.BasicLit:
				if arg.Kind == token.STRING {
					pattern, _ = strconv.Unquote(arg.Value)
				}
			case *ast.Ident:
				pattern = constants[arg.Name]
			}
			switch {
			case pattern == "":
				t.Errorf("%s: cannot resolve the pattern of this %s call; register routes with a string literal "+
					"or a string constant of this package, or teach openapi_drift_test.go the new form", where, sel.Sel.Name)
			case !strings.HasPrefix(pattern, "/"):
				t.Errorf("%s: pattern %q has a method or host part, which openapi_drift_test.go does not handle yet",
					where, pattern)
			default:
				routes = append(routes, sourceRoute{pattern: pattern, where: where})
			}
			return true
		})
	}
	if len(routes) == 0 {
		t.Fatal("found no route registrations in this package")
	}
	return routes
}

// newDriftCheckMux builds the mux exactly as Server.Start does, without
// middleware and without starting a listener. The admin API is attached so
// its routes are registered too.
func newDriftCheckMux(t *testing.T) *http.ServeMux {
	t.Helper()
	logger := logging.NewLogger(filepath.Join(t.TempDir(), "openapi-drift.log"), false)
	t.Cleanup(func() { _ = logger.Close() })
	s := &Server{config: &config.Config{}, logger: logger, adminAPI: &AdminAPI{}}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

// servingPattern returns the registered pattern that serves method+path, or
// "" when none does (no match, or only a redirect to a trailing-slash route).
func servingPattern(mux *http.ServeMux, method, path string) string {
	_, pattern := mux.Handler(httptest.NewRequest(method, path, nil))
	if pattern == path || (strings.HasSuffix(pattern, "/") && strings.HasPrefix(path, pattern)) {
		return pattern
	}
	return ""
}

// probeStatus calls the handler for method+path with an empty body and no
// dependencies wired. ok is false when the handler panicked, which happens
// for handlers that need a dependency before they look at the method.
func probeStatus(mux *http.ServeMux, method, path string) (code int, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder.Code, true
}
