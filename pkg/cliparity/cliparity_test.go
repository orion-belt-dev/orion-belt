package cliparity

import (
	"strings"
	"testing"
)

func TestEveryRouteIsClassified(t *testing.T) {
	routes, err := APIRoutes()
	if err != nil {
		t.Fatalf("parse API routes: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("no API routes parsed — the route parser is looking in the wrong place")
	}

	for _, route := range routes {
		if _, ok := Coverage[route.Key()]; !ok {
			t.Errorf("%s has no entry in Coverage: add the CLI command that exposes it, "+
				"or an Exempt reason explaining why osh/ocp/oadmin do not", route)
		}
	}
}

func TestNoStaleCoverageEntries(t *testing.T) {
	routes, err := APIRoutes()
	if err != nil {
		t.Fatalf("parse API routes: %v", err)
	}

	live := make(map[string]bool, len(routes))
	for _, route := range routes {
		live[route.Key()] = true
	}

	for key := range Coverage {
		if !live[key] {
			t.Errorf("Coverage lists %q, which is no longer registered in pkg/api — "+
				"remove the entry or fix the path", key)
		}
	}
}

func TestCoverageEntriesAreWellFormed(t *testing.T) {
	binaries := map[string]bool{"osh": true, "ocp": true, "oadmin": true}

	for key, cover := range Coverage {
		switch {
		case cover.Command == "" && cover.Exempt == "":
			t.Errorf("%s: needs either a Command or an Exempt reason", key)
		case cover.Command != "" && cover.Exempt != "":
			t.Errorf("%s: has both a Command (%q) and an Exempt reason (%q); pick one",
				key, cover.Command, cover.Exempt)
		case cover.Command != "" && !binaries[cover.Binary()]:
			t.Errorf("%s: command %q does not start with osh, ocp, or oadmin", key, cover.Command)
		}
		// Keys must match Route.Key()'s "METHOD /path" shape, or they can
		// never match a parsed route and the stale-entry check will not
		// explain why.
		if fields := strings.Fields(key); len(fields) != 2 || !strings.HasPrefix(fields[1], "/") {
			t.Errorf("%q is not a valid route key (want `METHOD /path`)", key)
		}
	}
}

func TestCommandsForFiltersByBinary(t *testing.T) {
	for _, binary := range []string{"osh", "ocp", "oadmin"} {
		commands := CommandsFor(binary)
		if len(commands) == 0 {
			t.Errorf("no covered commands recorded for %s", binary)
		}
		for _, command := range commands {
			if !strings.HasPrefix(command, binary+" ") && command != binary {
				t.Errorf("CommandsFor(%q) returned %q", binary, command)
			}
		}
	}
}

func TestParseAPIRoutesResolvesGroupPrefixes(t *testing.T) {
	routes, err := APIRoutes()
	if err != nil {
		t.Fatalf("parse API routes: %v", err)
	}

	want := map[string]bool{
		"POST /api/v1/admin/users":      false, // admin group
		"GET /api/v1/machines":          false, // protected group
		"POST /api/v1/public/login/key": false, // public group
		"GET /health":                   false, // bare router
		"GET /api/v1/mfa/status":        false, // registered from another file
	}
	for _, route := range routes {
		if _, ok := want[route.Key()]; ok {
			want[route.Key()] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("expected to parse route %q, but it was not found", key)
		}
	}
}
