package kube

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseScope(t *testing.T) {
	good := map[string]Scope{
		"payments/edit":         {Namespace: "payments", Role: "edit"},
		"*/view":                {Role: "view"},
		"*/cluster-admin":       {Role: "cluster-admin"},
		"kube-system/system:aa": {Namespace: "kube-system", Role: "system:aa"},
	}
	for in, want := range good {
		got, err := ParseScope(in)
		if err != nil || got != want {
			t.Errorf("ParseScope(%q) = %+v, %v; want %+v", in, got, err, want)
		}
		if got.String() != in {
			t.Errorf("round trip %q -> %q", in, got.String())
		}
	}
	for _, bad := range []string{"", "payments", "/edit", "payments/", "Payments/edit", "a/b/c", "ns/Edit", "-ns/edit", strings.Repeat("a", 64) + "/edit"} {
		if _, err := ParseScope(bad); err == nil {
			t.Errorf("ParseScope(%q) accepted", bad)
		}
	}
	if err := ValidateScopes(nil); err == nil {
		t.Error("no scopes accepted")
	}
	if err := ValidateScopes([]string{"a/view", "root"}); err == nil {
		t.Error("a remote user was accepted as a scope")
	}
}

func TestBindingNameStableAndDistinct(t *testing.T) {
	a := BindingName("12345678-aaaa", Scope{Namespace: "a", Role: "view"})
	if a != BindingName("12345678-aaaa", Scope{Namespace: "a", Role: "view"}) {
		t.Error("name not stable")
	}
	if a == BindingName("12345678-aaaa", Scope{Namespace: "b", Role: "view"}) {
		t.Error("two scopes of one grant share a name")
	}
	if !strings.HasPrefix(a, "orion-12345678-") || len(a) > 63 {
		t.Errorf("name %q", a)
	}
}

func TestDesiredAndDiff(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)
	past := now.Add(-time.Minute)
	desired := Desired([]Grant{
		{PermissionID: "p1", Username: "alice", Scopes: []string{"payments/edit", "*/view", "bogus"}, ExpiresAt: &later},
		{PermissionID: "p2", Username: "bob", Scopes: []string{"ops/admin"}, ExpiresAt: &past},
	})
	if len(desired) != 3 {
		t.Fatalf("desired = %d bindings, want 3 (bogus scope skipped)", len(desired))
	}
	for _, b := range desired {
		if b.Subject == "" || !strings.HasPrefix(b.Subject, SubjectPrefix) {
			t.Errorf("subject %q", b.Subject)
		}
	}
	edit, view, bobs := desired[0], desired[1], desired[2]
	if edit.Kind() != "RoleBinding" || view.Kind() != "ClusterRoleBinding" {
		t.Errorf("kinds %s %s", edit.Kind(), view.Kind())
	}

	extended := now.Add(2 * time.Hour)
	staleView := view
	staleView.ExpiresAt = &extended
	orphan := Binding{Name: "orion-deadbeef-00000000", Namespace: "x", ClusterRole: "view", Subject: "orion:eve", Permission: "gone"}
	actual := []Binding{staleView, orphan, bobs}

	p := Diff(desired, actual, now)
	if len(p.Create) != 1 || p.Create[0].Name != edit.Name {
		t.Errorf("create = %+v", p.Create)
	}
	if len(p.Update) != 1 || p.Update[0].Name != view.Name {
		t.Errorf("update = %+v", p.Update)
	}
	// The expired grant's binding and the orphan both go.
	if len(p.Delete) != 2 {
		t.Errorf("delete = %+v", p.Delete)
	}
	if p.Empty() {
		t.Error("plan reported empty")
	}
	if !Diff(nil, nil, now).Empty() {
		t.Error("empty diff not empty")
	}
	// A binding that already matches needs nothing.
	if q := Diff([]Binding{edit}, []Binding{edit}, now); !q.Empty() {
		t.Errorf("matching binding planned %+v", q)
	}
	noExpiry := edit
	noExpiry.ExpiresAt = nil
	if q := Diff([]Binding{noExpiry}, []Binding{edit}, now); len(q.Update) != 1 {
		t.Errorf("dropping the expiry is a change: %+v", q)
	}
}

// fakeAPI is a minimal API server for (cluster)rolebindings.
type fakeAPI struct {
	*httptest.Server
	mu      sync.Mutex
	objects map[string]object // path -> object
	headers []http.Header
	token   string
}

func newFakeAPI(t *testing.T) (*fakeAPI, Config) {
	t.Helper()
	f := &fakeAPI{objects: map[string]object{}, token: "sa-token"}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.Certificate().Raw})
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(f.token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, Config{APIServer: f.URL, TokenFile: tokenFile, CAFile: caFile}
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.headers = append(f.headers, r.Header.Clone())
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && (path == rbacPath+"/rolebindings" || path == rbacPath+"/clusterrolebindings"):
		kind := "RoleBinding"
		if strings.HasSuffix(path, "clusterrolebindings") {
			kind = "ClusterRoleBinding"
		}
		var items []object
		for _, o := range f.objects {
			if o.Kind == kind && o.Metadata.Labels[LabelManagedBy] == ManagedByValue {
				items = append(items, o)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	case r.Method == http.MethodPost:
		var o object
		_ = json.NewDecoder(r.Body).Decode(&o)
		key := path + "/" + o.Metadata.Name
		if _, ok := f.objects[key]; ok {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"already exists"}`))
			return
		}
		f.objects[key] = o
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPut:
		var o object
		_ = json.NewDecoder(r.Body).Decode(&o)
		f.objects[path] = o
	case r.Method == http.MethodDelete:
		if _, ok := f.objects[path]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.objects, path)
	default:
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	}
}

func TestClientApplyListDelete(t *testing.T) {
	f, cfg := newFakeAPI(t)
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	bs := Desired([]Grant{{PermissionID: "perm-1234", Username: "alice", Scopes: []string{"payments/edit", "*/view"}, ExpiresAt: &exp}})
	for _, b := range bs {
		if err := c.Apply(ctx, b); err != nil {
			t.Fatalf("apply %s: %v", b.Name, err)
		}
	}
	// Applying again replaces rather than failing.
	if err := c.Apply(ctx, bs[0]); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	got, err := c.List(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if q := Diff(bs, got, time.Now()); !q.Empty() {
		t.Errorf("listed bindings differ from applied: %+v", q)
	}
	for _, b := range got {
		if b.User != "alice" || b.Subject != "orion:alice" || b.Permission != "perm-1234" {
			t.Errorf("round trip lost fields: %+v", b)
		}
	}
	for _, b := range bs {
		if err := c.Delete(ctx, b); err != nil {
			t.Fatal(err)
		}
		if err := c.Delete(ctx, b); err != nil {
			t.Errorf("deleting twice: %v", err)
		}
	}
	if got, _ := c.List(ctx); len(got) != 0 {
		t.Errorf("left %+v", got)
	}

	// The agent touches nothing but its own bindings.
	if err := c.Apply(ctx, Binding{Name: "cluster-admin", ClusterRole: "cluster-admin"}); err == nil {
		t.Error("applied a binding without the orion- prefix")
	}
	if err := c.Apply(ctx, Binding{Name: "orion-x", ClusterRole: "view", Subject: "system:serviceaccount:kube-system:default"}); err == nil {
		t.Error("bound a subject that is not an Orion Belt user")
	}
	if err := c.Apply(ctx, Binding{Name: "orion-x", ClusterRole: "Bad Role", Subject: "orion:a"}); err == nil {
		t.Error("accepted an invalid ClusterRole")
	}
	if err := c.Delete(ctx, Binding{Name: "orion-x/../../y"}); err == nil {
		t.Error("deleted a path-traversing name")
	}
	if err := c.Delete(ctx, Binding{Name: "orion-x", Namespace: "Bad_NS"}); err == nil {
		t.Error("accepted an invalid namespace")
	}

	// API errors carry the server's message.
	f.mu.Lock()
	f.token = "rotated"
	f.mu.Unlock()
	if _, err := c.List(ctx); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("unauthorized List = %v", err)
	}
}

func TestNewClientConfigErrors(t *testing.T) {
	_, cfg := newFakeAPI(t)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if _, err := NewClient(Config{TokenFile: cfg.TokenFile, CAFile: cfg.CAFile}); err == nil {
		t.Error("no API server and not in a cluster")
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	c, err := NewClient(Config{TokenFile: cfg.TokenFile, CAFile: cfg.CAFile})
	if err != nil || c.Base().String() != "https://10.0.0.1:443" {
		t.Errorf("in-cluster default = %v, %v", c, err)
	}
	if _, err := NewClient(Config{APIServer: "http://insecure", TokenFile: cfg.TokenFile, CAFile: cfg.CAFile}); err == nil {
		t.Error("plain http API server accepted")
	}
	if _, err := NewClient(Config{APIServer: "https://x", TokenFile: cfg.TokenFile, CAFile: "/nonexistent"}); err == nil {
		t.Error("missing CA accepted")
	}
	if _, err := NewClient(Config{APIServer: "https://x", TokenFile: "/nonexistent", CAFile: cfg.CAFile}); err == nil {
		t.Error("missing token accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty.crt")
	_ = os.WriteFile(empty, []byte("not a cert"), 0o600)
	if _, err := NewClient(Config{APIServer: "https://x", TokenFile: cfg.TokenFile, CAFile: empty}); err == nil {
		t.Error("CA file without certificates accepted")
	}
}

func TestProxyImpersonatesAndStripsIdentity(t *testing.T) {
	f, cfg := newFakeAPI(t)
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := NewProxy(c, "alice", nil)

	// kubectl talks to the proxy over a stream, as over the SSH channel.
	client, agentSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		ServeStream(agentSide, h)
		close(done)
	}()
	req, _ := http.NewRequest(http.MethodGet, "http://kube/apis/rbac.authorization.k8s.io/v1/rolebindings", nil)
	req.Header.Set("Authorization", "Bearer stolen-admin-token")
	req.Header.Set("Impersonate-User", "system:admin")
	req.Header.Set("Impersonate-Group", "system:masters")
	req.Header.Set("Impersonate-Extra-Scopes", "x")
	if err := req.Write(client); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(client), req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	client.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStream did not return after the client closed")
	}

	f.mu.Lock()
	got := f.headers[len(f.headers)-1]
	f.mu.Unlock()
	if got.Get("Authorization") != "Bearer sa-token" {
		t.Errorf("Authorization = %q, want the agent's service account", got.Get("Authorization"))
	}
	if got.Get("Impersonate-User") != "orion:alice" {
		t.Errorf("Impersonate-User = %q", got.Get("Impersonate-User"))
	}
	for name := range got {
		if strings.HasPrefix(strings.ToLower(name), "impersonate-") && name != "Impersonate-User" {
			t.Errorf("client header %s reached the API server", name)
		}
	}
}

func TestProxyReportsUnreachableAPI(t *testing.T) {
	f, cfg := newFakeAPI(t)
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	var reported error
	h := NewProxy(c, "alice", func(err error) { reported = err })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))
	if rec.Code != http.StatusBadGateway || reported == nil {
		t.Errorf("code %d, reported %v", rec.Code, reported)
	}
}

func TestWireResponses(t *testing.T) {
	var buf strings.Builder
	_ = WriteResponse(&buf, []Binding{{Name: "orion-a"}}, nil)
	got, err := ReadResponse(strings.NewReader(buf.String()))
	if err != nil || len(got) != 1 {
		t.Errorf("ReadResponse = %v, %v", got, err)
	}
	buf.Reset()
	_ = WriteResponse(&buf, nil, io.ErrUnexpectedEOF)
	if _, err := ReadResponse(strings.NewReader(buf.String())); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Errorf("error not relayed: %v", err)
	}
	if _, err := ReadResponse(strings.NewReader("garbage")); err == nil {
		t.Error("garbage accepted")
	}
}
