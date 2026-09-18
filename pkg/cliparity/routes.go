// Package cliparity keeps the client CLIs (osh, ocp, oadmin) honest about the
// HTTP API surface.
//
// Every route registered in pkg/api must have an entry in Coverage saying
// either which CLI command exposes it or why the CLI deliberately does not.
// A new endpoint added by any theme therefore fails the parity test until
// somebody makes that call, which is what stops the CLI from silently falling
// behind the web console and the API.
package cliparity

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Route is one HTTP route registered on the API router.
type Route struct {
	Method string
	Path   string
	// Group is the router group variable the route was registered on, e.g.
	// "admin". Informational; Key() already includes the resolved prefix.
	Group string
	// File and Line locate the registration, so failures point at the code.
	File string
	Line int
}

// Key is the canonical identifier used by Coverage: "METHOD /full/path".
func (r Route) Key() string { return r.Method + " " + r.Path }

// String renders a route with its source location for test failures.
func (r Route) String() string {
	return fmt.Sprintf("%s (%s:%d)", r.Key(), r.File, r.Line)
}

// httpMethods are the gin router methods that register a route. "Any" is
// included so catch-all handlers (plugin webhooks) are classified too.
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true, "Any": true,
}

// groupPrefixes maps a router-group variable name to the path prefix routes
// registered on it inherit.
//
// Gin groups are passed between files (registerMFARoutes(protected), …), so
// resolving prefixes by following assignments across the package would mean
// interprocedural analysis. The API instead uses a fixed, small set of group
// names consistently, so a name table is both simpler and stricter: an
// unrecognized group name is an error rather than a silently mis-prefixed
// route.
var groupPrefixes = map[string]string{
	"s.router":  "",
	"api.route": "",
	"v1":        "/api/v1",
	"public":    "/api/v1/public",
	"protected": "/api/v1",
	"admin":     "/api/v1/admin",
}

// ParseAPIRoutes returns every route registered under dir (typically pkg/api),
// sorted by key. It reports an error when a route is registered on a router
// group this package does not know how to prefix.
func ParseAPIRoutes(dir string) ([]Route, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	var routes []Route
	var unknown []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !httpMethods[sel.Sel.Name] {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			routePath, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.HasPrefix(routePath, "/") {
				return true
			}

			group := exprName(sel.X)
			prefix, known := groupPrefixes[group]
			pos := fset.Position(lit.Pos())
			if !known {
				unknown = append(unknown, fmt.Sprintf("%s.%s(%q) at %s:%d",
					group, sel.Sel.Name, routePath, filepath.Base(pos.Filename), pos.Line))
				return true
			}

			routes = append(routes, Route{
				Method: sel.Sel.Name,
				Path:   joinPath(prefix, routePath),
				Group:  group,
				File:   filepath.Base(pos.Filename),
				Line:   pos.Line,
			})
			return true
		})
	}

	if len(unknown) > 0 {
		return nil, fmt.Errorf("routes registered on unknown router groups (add them to groupPrefixes): %s",
			strings.Join(unknown, ", "))
	}

	sort.Slice(routes, func(i, j int) bool { return routes[i].Key() < routes[j].Key() })
	return routes, nil
}

// exprName renders the receiver of a route registration ("admin",
// "s.router") so it can be looked up in groupPrefixes.
func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprName(v.X) + "." + v.Sel.Name
	default:
		return ""
	}
}

func joinPath(prefix, path string) string {
	joined := strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(path, "/")
	if len(joined) > 1 {
		joined = strings.TrimSuffix(joined, "/")
	}
	if joined == "" {
		return "/"
	}
	return joined
}

// RepoRoot walks up from the current directory to the module root, so tests
// can locate pkg/api regardless of which package they run in.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// APIRoutes parses the routes of the API package in this repository.
func APIRoutes() ([]Route, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}
	return ParseAPIRoutes(filepath.Join(root, "pkg", "api"))
}
