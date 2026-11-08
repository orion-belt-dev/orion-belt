package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

var updateRoutes = flag.Bool("update-routes", false, "rewrite sdk/routes.json from the router")

// routesManifest is the checked-in list of /api/v1 routes. The Python and
// TypeScript SDK test suites read it to prove they cover every endpoint, so it
// has to follow the router exactly.
const routesManifest = "../../sdk/routes.json"

type manifestRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// apiRoutes lists the router's /api/v1 routes. A path registered for every
// method (gin's Any) is collapsed to one "ANY" entry.
func apiRoutes(t *testing.T) []manifestRoute {
	t.Helper()
	// Route registration touches no dependency, so a bare server is enough.
	// WebAuthn routes exist only when WebAuthn is configured; turn it on so the
	// manifest lists every route a server can expose.
	wa, err := webauthn.New(&webauthn.Config{RPDisplayName: "Orion Belt", RPID: "localhost", RPOrigins: []string{"https://localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewAPIServer(nil, nil, common.NewLogger(common.ERROR), Options{JWTSecret: "routes-manifest", WebAuthn: wa})

	methods := map[string][]string{}
	for _, r := range s.Router().Routes() {
		if strings.HasPrefix(r.Path, "/api/v1/") {
			methods[r.Path] = append(methods[r.Path], r.Method)
		}
	}
	var out []manifestRoute
	for path, ms := range methods {
		// gin's Any registers nine methods; nothing else registers more than two.
		if len(ms) >= 9 {
			out = append(out, manifestRoute{"ANY", path})
			continue
		}
		for _, m := range ms {
			out = append(out, manifestRoute{m, path})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

func TestRoutesManifest_MatchesRouter(t *testing.T) {
	routes := apiRoutes(t)
	want, err := json.MarshalIndent(routes, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')

	path := filepath.FromSlash(routesManifest)
	if *updateRoutes {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is out of date with the router: run `go test ./pkg/api -run TestRoutesManifest -update-routes`, then cover the new routes in sdk/python and sdk/js", path)
	}
}
