package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// fakeStore is a minimal in-memory database.Store for exercising
// handlePublicKeyAuth's cert dispatch without a real Postgres instance.
type fakeStore struct {
	database.Store

	mu       sync.Mutex
	users    map[string]*common.User // by username
	machines map[string]*common.Machine
	caKeys   map[string]*common.CAKey
	caPriv   map[string][]byte
	certs    map[string]*common.SSHCertificate
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:    make(map[string]*common.User),
		machines: make(map[string]*common.Machine),
		caKeys:   make(map[string]*common.CAKey),
		caPriv:   make(map[string][]byte),
		certs:    make(map[string]*common.SSHCertificate),
	}
}

func (f *fakeStore) CreateUser(_ context.Context, u *common.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.Username] = u
	return nil
}

func (f *fakeStore) GetUserByUsername(_ context.Context, username string) (*common.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[username]
	if !ok {
		return nil, database.ErrNotFound
	}
	return u, nil
}

func (f *fakeStore) CreateMachine(_ context.Context, m *common.Machine) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.machines[m.Name] = m
	return nil
}

func (f *fakeStore) GetMachine(_ context.Context, id string) (*common.Machine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.machines {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, database.ErrNotFound
}

func (f *fakeStore) GetMachineByName(_ context.Context, name string) (*common.Machine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.machines[name]
	if !ok {
		return nil, database.ErrNotFound
	}
	return m, nil
}

func (f *fakeStore) CreateCAKey(_ context.Context, key *common.CAKey, privEnc []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.caKeys[key.CAType] = key
	f.caPriv[key.CAType] = privEnc
	return nil
}

func (f *fakeStore) GetActiveCAKey(_ context.Context, caType string) (*common.CAKey, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.caKeys[caType]
	if !ok {
		return nil, nil, database.ErrNotFound
	}
	return key, f.caPriv[caType], nil
}

func (f *fakeStore) ListCAKeys(_ context.Context, caType string) ([]*common.CAKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if key, ok := f.caKeys[caType]; ok {
		return []*common.CAKey{key}, nil
	}
	return nil, nil
}

func (f *fakeStore) CreateSSHCertificate(_ context.Context, cert *common.SSHCertificate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.certs[cert.Serial] = cert
	return nil
}

func (f *fakeStore) GetSSHCertificateBySerial(_ context.Context, serial string) (*common.SSHCertificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.certs[serial]
	if !ok {
		return nil, database.ErrNotFound
	}
	return c, nil
}

func (f *fakeStore) ListSSHCertificates(_ context.Context, _ common.SSHCertFilter, _, _ int) ([]*common.SSHCertificate, error) {
	return nil, nil
}

func (f *fakeStore) ListRevokedCertSerials(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for serial, c := range f.certs {
		if c.RevokedAt != nil && c.ExpiresAt.After(time.Now()) {
			out = append(out, serial)
		}
	}
	return out, nil
}

func (f *fakeStore) RevokeSSHCertificate(_ context.Context, serial, revokedBy, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.certs[serial]
	if !ok {
		return database.ErrNotFound
	}
	now := time.Now()
	c.RevokedAt = &now
	c.RevokedBy = &revokedBy
	c.RevokeReason = reason
	return nil
}

// testServer builds a Server wired to the fake store. It takes testing.TB so
// the benchmarks in bench_test.go can share the harness with the tests.
func testServer(tb testing.TB, store *fakeStore) (*Server, *ca.Authority) {
	tb.Helper()
	return testServerWithLogger(tb, store, common.NewLogger(common.INFO))
}

// testServerWithLogger is testServer with the log destination chosen by the
// caller — the benchmarks discard log output to keep their stdout parseable.
func testServerWithLogger(tb testing.TB, store *fakeStore, logger *common.Logger) (*Server, *ca.Authority) {
	tb.Helper()
	authority, err := ca.New(common.SSHCAConfig{Enabled: true, MasterKey: "0123456789abcdef0123456789abcdef"}, store, logger)
	if err != nil {
		tb.Fatalf("ca.New: %v", err)
	}
	return &Server{
		store:         store,
		ca:            authority,
		pluginManager: plugin.NewManager(logger),
		logger:        logger,
		config:        &common.Config{},
	}, authority
}

func genKey(tb testing.TB) (ssh.Signer, ssh.PublicKey) {
	tb.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		tb.Fatalf("wrap signer: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		tb.Fatalf("wrap pubkey: %v", err)
	}
	return signer, sshPub
}

// dialWithSigner drives handlePublicKeyAuth through a real SSH handshake
// over a loopback TCP connection, returning the resulting Permissions
// (success) or the handshake error (failure) — exercising the exact code
// path a real client/agent connection takes, not just the internal Go
// call. Uses real TCP (not net.Pipe) so a rejected/errored handshake on
// one side can't deadlock the other waiting on an unbuffered synchronous
// pipe write.
func dialWithSigner(t *testing.T, srv *Server, sshUser string, signer ssh.Signer) (*ssh.Permissions, error) {
	t.Helper()

	hostSigner, _ := genKey(t)
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: srv.handlePublicKeyAuth}
	serverConfig.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	type serverResult struct {
		perms *ssh.Permissions
		err   error
	}
	resultCh := make(chan serverResult, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			resultCh <- serverResult{err: err}
			return
		}
		defer conn.Close()
		sc, chans, reqs, err := ssh.NewServerConn(conn, serverConfig)
		if err != nil {
			resultCh <- serverResult{err: err}
			return
		}
		defer sc.Close()
		go ssh.DiscardRequests(reqs)
		go func() {
			for nc := range chans {
				_ = nc.Reject(ssh.UnknownChannelType, "test server")
			}
		}()
		resultCh <- serverResult{perms: sc.Permissions}
	}()

	clientConfig := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	clientConn, err := ssh.Dial("tcp", ln.Addr().String(), clientConfig)

	select {
	case res := <-resultCh:
		if clientConn != nil {
			clientConn.Close()
		}
		if err != nil {
			return nil, err
		}
		return res.perms, res.err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server-side handshake result")
		return nil, nil
	}
}

func mustCertSigner(tb testing.TB, cert *ssh.Certificate, key ssh.Signer) ssh.Signer {
	tb.Helper()
	signer, err := ssh.NewCertSigner(cert, key)
	if err != nil {
		tb.Fatalf("NewCertSigner: %v", err)
	}
	return signer
}

func TestHandlePublicKeyAuth_UserCert(t *testing.T) {
	store := newFakeStore()
	srv, authority := testServer(t, store)

	user := common.NewUser("alice", "alice@example.com", "", false)
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	key, pub := genKey(t)
	cert, err := authority.IssueUserCert(context.Background(), user.ID, "alice", pub, 0)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}
	certSigner := mustCertSigner(t, cert, key)

	perms, err := dialWithSigner(t, srv, "alice", certSigner)
	if err != nil {
		t.Fatalf("expected successful auth, got: %v", err)
	}
	if perms.Extensions["cert_type"] != "user" {
		t.Errorf("cert_type = %q, want %q", perms.Extensions["cert_type"], "user")
	}
	if perms.Extensions["user_id"] != user.ID {
		t.Errorf("user_id = %q, want %q", perms.Extensions["user_id"], user.ID)
	}
}

func TestHandlePublicKeyAuth_UserCertWrongPrincipalRejected(t *testing.T) {
	store := newFakeStore()
	srv, authority := testServer(t, store)

	alice := common.NewUser("alice", "alice@example.com", "", false)
	bob := common.NewUser("bob", "bob@example.com", "", false)
	store.CreateUser(context.Background(), alice)
	store.CreateUser(context.Background(), bob)

	key, pub := genKey(t)
	cert, err := authority.IssueUserCert(context.Background(), alice.ID, "alice", pub, 0)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}
	certSigner := mustCertSigner(t, cert, key)

	// Present alice's cert while claiming to be bob.
	if _, err := dialWithSigner(t, srv, "bob", certSigner); err == nil {
		t.Fatal("expected auth to fail when SSH username doesn't match the cert's principal")
	}
}

func TestHandlePublicKeyAuth_HostCert(t *testing.T) {
	store := newFakeStore()
	srv, authority := testServer(t, store)

	machine := common.NewMachine("web-01", "10.0.0.5", 22, nil)
	if err := store.CreateMachine(context.Background(), machine); err != nil {
		t.Fatalf("CreateMachine: %v", err)
	}

	key, pub := genKey(t)
	cert, err := authority.IssueHostCert(context.Background(), machine.ID, []string{"web-01"}, pub, 0)
	if err != nil {
		t.Fatalf("IssueHostCert: %v", err)
	}
	certSigner := mustCertSigner(t, cert, key)

	perms, err := dialWithSigner(t, srv, "web-01", certSigner)
	if err != nil {
		t.Fatalf("expected successful auth, got: %v", err)
	}
	if perms.Extensions["cert_type"] != "host" {
		t.Errorf("cert_type = %q, want %q", perms.Extensions["cert_type"], "host")
	}
	if perms.Extensions["machine_id"] != machine.ID {
		t.Errorf("machine_id = %q, want %q", perms.Extensions["machine_id"], machine.ID)
	}
}

func TestHandlePublicKeyAuth_HostCertUnregisteredMachineRejected(t *testing.T) {
	store := newFakeStore()
	srv, authority := testServer(t, store)

	key, pub := genKey(t)
	cert, err := authority.IssueHostCert(context.Background(), "some-machine-id", []string{"ghost-01"}, pub, 0)
	if err != nil {
		t.Fatalf("IssueHostCert: %v", err)
	}
	certSigner := mustCertSigner(t, cert, key)

	if _, err := dialWithSigner(t, srv, "ghost-01", certSigner); err == nil {
		t.Fatal("expected auth to fail for a host cert whose machine isn't registered")
	}
}

func TestHandlePublicKeyAuth_UntrustedCAsRejected(t *testing.T) {
	store := newFakeStore()
	srv, _ := testServer(t, store)

	user := common.NewUser("alice", "alice@example.com", "", false)
	store.CreateUser(context.Background(), user)

	// A second, unrelated CA — its certs must never be trusted by srv.
	rogueStore := newFakeStore()
	rogueCA, err := ca.New(common.SSHCAConfig{Enabled: true, MasterKey: "fedcba9876543210fedcba9876543210"}, rogueStore, nil)
	if err != nil {
		t.Fatalf("ca.New (rogue): %v", err)
	}

	key, pub := genKey(t)
	cert, err := rogueCA.IssueUserCert(context.Background(), user.ID, "alice", pub, 0)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}
	certSigner := mustCertSigner(t, cert, key)

	if _, err := dialWithSigner(t, srv, "alice", certSigner); err == nil {
		t.Fatal("expected auth to fail for a cert signed by an untrusted CA")
	}
}

func TestHandlePublicKeyAuth_RevokedCertRejected(t *testing.T) {
	store := newFakeStore()
	srv, authority := testServer(t, store)

	user := common.NewUser("alice", "alice@example.com", "", false)
	store.CreateUser(context.Background(), user)

	key, pub := genKey(t)
	cert, err := authority.IssueUserCert(context.Background(), user.ID, "alice", pub, 0)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}
	certSigner := mustCertSigner(t, cert, key)

	// Sanity: works before revocation.
	if _, err := dialWithSigner(t, srv, "alice", certSigner); err != nil {
		t.Fatalf("expected auth to succeed before revocation, got: %v", err)
	}

	if err := authority.RevokeCertificate(context.Background(), strconv.FormatUint(cert.Serial, 10), "admin-1", "test"); err != nil {
		t.Fatalf("RevokeCertificate: %v", err)
	}

	if _, err := dialWithSigner(t, srv, "alice", certSigner); err == nil {
		t.Fatal("expected auth to fail after revocation")
	}
}

func TestHandlePublicKeyAuth_CANotEnabled(t *testing.T) {
	store := newFakeStore()
	logger := common.NewLogger(common.INFO)
	srv := &Server{
		store:         store,
		ca:            nil, // SSH CA disabled
		pluginManager: plugin.NewManager(logger),
		logger:        logger,
		config:        &common.Config{},
	}

	key, pub := genKey(t)
	authority, err := ca.New(common.SSHCAConfig{Enabled: true, MasterKey: "0123456789abcdef0123456789abcdef"}, newFakeStore(), nil)
	if err != nil {
		t.Fatalf("ca.New: %v", err)
	}
	user := common.NewUser("alice", "alice@example.com", "", false)
	cert, err := authority.IssueUserCert(context.Background(), user.ID, "alice", pub, 0)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}
	certSigner := mustCertSigner(t, cert, key)

	if _, err := dialWithSigner(t, srv, "alice", certSigner); err == nil {
		t.Fatal("expected cert auth to be rejected when the server has SSH CA disabled")
	}
}
