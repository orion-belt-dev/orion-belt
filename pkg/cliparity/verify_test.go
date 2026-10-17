package cliparity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func noop(*cobra.Command, []string) {}

// testTree builds:
//
//	tool [--verbose persistent]
//	  users (group, alias "u")
//	    list [--limit]
//	  stub (neither runnable nor a group)
func testTree() *cobra.Command {
	root := &cobra.Command{Use: "tool", Short: "root", Long: "Test tool."}
	root.PersistentFlags().Bool("verbose", false, "chatty output")

	users := &cobra.Command{Use: "users", Aliases: []string{"u"}, Short: "users", Long: "Manage users."}
	list := &cobra.Command{Use: "list", Short: "list users", Run: noop}
	list.Flags().Int("limit", 0, "max rows")
	users.AddCommand(list)

	root.AddCommand(users, &cobra.Command{Use: "stub", Short: "does nothing"})
	return root
}

func TestVerifyInvocation(t *testing.T) {
	cases := []struct {
		invocation string
		wantErr    string
	}{
		{"tool users list", ""},
		{"tool u list --limit", ""},
		{"tool users list --verbose", ""}, // inherited persistent flag
		{"tool users", ""},                // a group is a valid target
		{"tool", ""},
		{"", "is not an invocation of tool"},
		{"other users", "is not an invocation of tool"},
		{"tool users delete", `has no subcommand "delete"`},
		{"tool users list --force", "has no flag --force"},
		{"tool stub", "which does nothing"},
	}
	for _, tc := range cases {
		t.Run(tc.invocation, func(t *testing.T) {
			err := verifyInvocation(testTree(), "tool", tc.invocation)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("verifyInvocation(%q) = %v, want nil", tc.invocation, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("verifyInvocation(%q) = %v, want error containing %q", tc.invocation, err, tc.wantErr)
			}
		})
	}
}

func TestVerifyCommands_EmptyTreeReportsEveryCoveredInvocation(t *testing.T) {
	want := CommandsFor("ocp")
	if len(want) == 0 {
		t.Fatal("test premise broken: Coverage has no ocp invocations")
	}
	errs := VerifyCommands(&cobra.Command{Use: "ocp"}, "ocp")
	if len(errs) != len(want) {
		t.Errorf("VerifyCommands on an empty tree returned %d errors, want one per invocation (%d)", len(errs), len(want))
	}
}

func TestVerifyHelp(t *testing.T) {
	root := testTree()
	if errs := VerifyHelp(root); len(errs) != 0 {
		t.Fatalf("VerifyHelp on a documented tree = %v, want none", errs)
	}

	bad := &cobra.Command{Use: "bad"} // no Short
	bad.Flags().String("name", "", "")
	group := &cobra.Command{Use: "group", Short: "a group without Long"}
	group.AddCommand(&cobra.Command{Use: "leaf", Short: "leaf", Run: noop})
	root.AddCommand(bad, group)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	errs := VerifyHelp(root)
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{
		`"tool bad" has no Short description`,
		`"tool bad": flag --name has no usage text`,
		`"tool group" is a command group and needs a Long description`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("VerifyHelp errors:\n%s\nwant one containing %q", joined, want)
		}
	}
	if len(errs) != 3 {
		t.Errorf("VerifyHelp returned %d errors, want exactly 3 (help/completion are exempt)", len(errs))
	}
}

func TestRouteString_IncludesLocation(t *testing.T) {
	r := Route{Method: "GET", Path: "/x", File: "a.go", Line: 7}
	if got := r.String(); got != "GET /x (a.go:7)" {
		t.Errorf("String() = %q", got)
	}
}

func writeGo(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseAPIRoutes_SyntheticPackage(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "routes.go", `package api
func register() {
	admin.GET("/users/", h)
	s.router.Any("/", h)
	v1.POST(path, h)        // non-literal path: ignored
	v1.GET("relative", h)   // not absolute: ignored
	v1.GET(`+"`/raw`"+`, h)
	helper()                 // no args
	x.Do("/nope", h)         // not an HTTP method
}`)
	writeGo(t, dir, "routes_test.go", `package api
func t() { unknown.GET("/ignored", h) }`)
	writeGo(t, dir, "README.md", "not go")
	if err := os.Mkdir(filepath.Join(dir, "sub.go"), 0o700); err != nil {
		t.Fatal(err)
	}

	routes, err := ParseAPIRoutes(dir)
	if err != nil {
		t.Fatalf("ParseAPIRoutes: %v", err)
	}
	var keys []string
	for _, r := range routes {
		keys = append(keys, r.Key())
	}
	want := []string{"Any /", "GET /api/v1/admin/users", "GET /api/v1/raw"}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Errorf("routes = %v, want %v", keys, want)
	}
}

func TestParseAPIRoutes_Errors(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		if _, err := ParseAPIRoutes(filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Error("ParseAPIRoutes on a missing dir succeeded")
		}
	})
	t.Run("syntax error", func(t *testing.T) {
		dir := t.TempDir()
		writeGo(t, dir, "bad.go", "package api\nfunc {")
		if _, err := ParseAPIRoutes(dir); err == nil || !strings.Contains(err.Error(), "parse") {
			t.Errorf("ParseAPIRoutes error = %v, want a parse error", err)
		}
	})
	t.Run("unknown router group", func(t *testing.T) {
		dir := t.TempDir()
		writeGo(t, dir, "r.go", "package api\nfunc f() { mystery.GET(\"/x\", h); f().GET(\"/y\", h) }")
		_, err := ParseAPIRoutes(dir)
		if err == nil || !strings.Contains(err.Error(), `mystery.GET("/x") at r.go:2`) || !strings.Contains(err.Error(), `.GET("/y")`) {
			t.Errorf("ParseAPIRoutes error = %v, want both unknown registrations named", err)
		}
	})
}

func TestJoinPath(t *testing.T) {
	cases := map[[2]string]string{
		{"", "/"}:          "/",
		{"", ""}:           "/",
		{"/api/v1", "/x/"}: "/api/v1/x",
		{"/api/v1/", "x"}:  "/api/v1/x",
		{"/api/v1", "/"}:   "/api/v1",
		{"", "/health"}:    "/health",
	}
	for in, want := range cases {
		if got := joinPath(in[0], in[1]); got != want {
			t.Errorf("joinPath(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestRepoRootAndAPIRoutes_OutsideModule_ReturnError(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := RepoRoot(); err == nil || !strings.Contains(err.Error(), "go.mod not found") {
		t.Errorf("RepoRoot error = %v, want go.mod not found", err)
	}
	if _, err := APIRoutes(); err == nil {
		t.Error("APIRoutes outside the module succeeded")
	}
}
