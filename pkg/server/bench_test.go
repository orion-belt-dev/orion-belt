package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/zrougamed/orion-belt/pkg/auth"
	"github.com/zrougamed/orion-belt/pkg/ca"
	"github.com/zrougamed/orion-belt/pkg/common"
	"github.com/zrougamed/orion-belt/pkg/plugin"
	"github.com/zrougamed/orion-belt/pkg/recording"
)

// Gateway performance benchmarks.
//
// Two things are measured, matching the two ways the gateway can get slow:
//
//   - Control plane — session establishment: TCP connect + SSH handshake +
//     publickey/certificate auth, i.e. everything between "user runs ssh" and
//     "the connection is authenticated". This is the latency a user feels on
//     every new session, and it is dominated by code we own (cert validation,
//     CA checks, store lookups) plus the crypto handshake.
//
//   - Data plane — proxied bytes: the client<->agent copy loop, with and
//     without session recording in the path. This is what determines whether
//     an interactive session feels responsive under output-heavy commands.
//
// Everything runs against the in-memory fakeStore over loopback TCP, so there
// is no Postgres or network variance in the numbers: a change here means a
// change in our code. See docs/BENCHMARKS.md.

// -----------------------------------------------------------------------------
// Control plane: session establishment
// -----------------------------------------------------------------------------

// benchLogger logs at the gateway's normal level but discards the output, so
// per-call log formatting is still charged to the benchmark while the results
// on stdout stay parseable.
func benchLogger() *common.Logger {
	return common.NewLoggerTo(common.INFO, io.Discard)
}

// benchGateway is a running SSH gateway listener wired to the real
// handlePublicKeyAuth dispatch, so each dial exercises the production auth
// path rather than an in-process function call.
type benchGateway struct {
	srv      *Server
	listener net.Listener
	wg       sync.WaitGroup
}

func newBenchGateway(tb testing.TB, store *fakeStore) (*benchGateway, *ca.Authority) {
	tb.Helper()

	srv, authority := testServerWithLogger(tb, store, benchLogger())
	// testServer leaves authService nil (its tests only exercise the cert
	// paths); the legacy static-pubkey benchmark needs it.
	srv.authService = auth.NewAuthService(store, srv.logger)
	srv.pluginManager = plugin.NewManager(srv.logger)

	hostSigner, _ := genKey(tb)
	cfg := &ssh.ServerConfig{PublicKeyCallback: srv.handlePublicKeyAuth}
	cfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}

	g := &benchGateway{srv: srv, listener: listener}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			discardTimeWait(conn)
			g.wg.Add(1)
			go func() {
				defer g.wg.Done()
				defer conn.Close()
				sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					_ = nc.Reject(ssh.UnknownChannelType, "benchmark gateway")
				}
			}()
		}
	}()

	// Closing the listener unblocks Accept and ends the accept loop; the
	// per-connection goroutines end when their client disconnects.
	tb.Cleanup(func() {
		listener.Close()
		g.wg.Wait()
	})

	return g, authority
}

// discardTimeWait makes Close send RST instead of FIN, so the socket is
// released immediately rather than sitting in TIME_WAIT.
//
// These benchmarks open tens of thousands of short-lived loopback connections.
// With a normal close, every one of them holds an ephemeral port for the
// TIME_WAIT interval (30-120s depending on the OS), the ~16k-port range is
// exhausted within a run, and dials start failing with "can't assign requested
// address" — which is a property of the measurement harness, not of the
// gateway. Load generators drop TIME_WAIT for exactly this reason. It affects
// only how the connection is torn down, not the establishment being measured.
func discardTimeWait(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
}

// establish performs one full session establishment and tears it down.
// Dialling is done by hand rather than via ssh.Dial so the TCP connection can
// be configured before the SSH handshake runs on top of it.
func (g *benchGateway) establish(sshUser string, signer ssh.Signer) error {
	addr := g.listener.Addr().String()

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	discardTimeWait(conn)

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	if err != nil {
		conn.Close()
		return err
	}

	return ssh.NewClient(sshConn, chans, reqs).Close()
}

// benchIdentity is one authenticated principal the gateway will accept.
type benchIdentity struct {
	sshUser string
	signer  ssh.Signer
}

// legacyPubkeyIdentity registers a user whose static public key is the
// credential — the pre-SSH-CA path most deployments still use.
func legacyPubkeyIdentity(tb testing.TB, store *fakeStore) benchIdentity {
	tb.Helper()
	signer, pub := genKey(tb)
	user := common.NewUser("bench-user", "bench@example.com", "", false)
	user.PublicKey = string(ssh.MarshalAuthorizedKey(pub))
	if err := store.CreateUser(context.Background(), user); err != nil {
		tb.Fatalf("CreateUser: %v", err)
	}
	return benchIdentity{sshUser: user.Username, signer: signer}
}

// userCertIdentity registers a user and issues them a short-lived User-CA
// certificate — the SSH CA client path.
func userCertIdentity(tb testing.TB, store *fakeStore, authority *ca.Authority) benchIdentity {
	tb.Helper()
	key, pub := genKey(tb)
	user := common.NewUser("bench-cert-user", "bench-cert@example.com", "", false)
	if err := store.CreateUser(context.Background(), user); err != nil {
		tb.Fatalf("CreateUser: %v", err)
	}
	cert, err := authority.IssueUserCert(context.Background(), user.ID, user.Username, pub, 0)
	if err != nil {
		tb.Fatalf("IssueUserCert: %v", err)
	}
	return benchIdentity{sshUser: user.Username, signer: mustCertSigner(tb, cert, key)}
}

// hostCertIdentity registers a machine and issues its agent a Host-CA
// certificate — the reverse-dial agent path.
func hostCertIdentity(tb testing.TB, store *fakeStore, authority *ca.Authority) benchIdentity {
	tb.Helper()
	key, pub := genKey(tb)
	machine := common.NewMachine("bench-host-01", "10.0.0.5", 22, nil)
	if err := store.CreateMachine(context.Background(), machine); err != nil {
		tb.Fatalf("CreateMachine: %v", err)
	}
	cert, err := authority.IssueHostCert(context.Background(), machine.ID, []string{machine.Name}, pub, 0)
	if err != nil {
		tb.Fatalf("IssueHostCert: %v", err)
	}
	return benchIdentity{sshUser: machine.Name, signer: mustCertSigner(tb, cert, key)}
}

// benchAuthPaths returns, per credential type the gateway accepts, a setup
// function that builds a gateway already primed to authenticate it. Setup is
// deferred rather than done eagerly because Go re-invokes a benchmark body as
// it ramps b.N, and each invocation needs its own gateway.
func benchAuthPaths() map[string]func(testing.TB) (*benchGateway, benchIdentity) {
	return map[string]func(testing.TB) (*benchGateway, benchIdentity){
		"legacy_pubkey": func(tb testing.TB) (*benchGateway, benchIdentity) {
			store := newFakeStore()
			g, _ := newBenchGateway(tb, store)
			return g, legacyPubkeyIdentity(tb, store)
		},
		"user_cert": func(tb testing.TB) (*benchGateway, benchIdentity) {
			store := newFakeStore()
			g, authority := newBenchGateway(tb, store)
			return g, userCertIdentity(tb, store, authority)
		},
		"host_cert": func(tb testing.TB) (*benchGateway, benchIdentity) {
			store := newFakeStore()
			g, authority := newBenchGateway(tb, store)
			return g, hostCertIdentity(tb, store, authority)
		},
	}
}

// benchAuthPathNames keeps sub-benchmark ordering stable across runs so the
// baseline file diffs cleanly.
var benchAuthPathNames = []string{"legacy_pubkey", "user_cert", "host_cert"}

// BenchmarkSessionEstablish measures single-session establishment latency for
// each credential type: ns/op is the end-to-end cost of one authenticated
// session, serially, with no contention.
func BenchmarkSessionEstablish(b *testing.B) {
	paths := benchAuthPaths()
	for _, name := range benchAuthPathNames {
		b.Run(name, func(b *testing.B) {
			g, id := paths[name](b)

			// Warm up: the first handshake pays one-time costs (CA key
			// decrypt, revocation list load) that would otherwise skew a
			// short run.
			if err := g.establish(id.sshUser, id.signer); err != nil {
				b.Fatalf("warmup establish: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := g.establish(id.sshUser, id.signer); err != nil {
					b.Fatalf("establish: %v", err)
				}
			}
		})
	}
}

// BenchmarkSessionEstablishUnderLoad measures establishment throughput and
// latency spread with a fixed number of clients connecting concurrently.
//
// Concurrency is a fixed worker count rather than b.RunParallel's
// GOMAXPROCS-scaled parallelism, so the same numbers mean the same thing on a
// 4-core CI runner and a 16-core laptop — a prerequisite for comparing runs
// over time.
func BenchmarkSessionEstablishUnderLoad(b *testing.B) {
	paths := benchAuthPaths()
	for _, name := range benchAuthPathNames {
		for _, workers := range []int{8, 32, 64} {
			b.Run(fmt.Sprintf("%s/clients_%d", name, workers), func(b *testing.B) {
				g, id := paths[name](b)
				if err := g.establish(id.sshUser, id.signer); err != nil {
					b.Fatalf("warmup establish: %v", err)
				}
				benchEstablishConcurrent(b, g, id, workers)
			})
		}
	}
}

func benchEstablishConcurrent(b *testing.B, g *benchGateway, id benchIdentity, workers int) {
	b.Helper()

	remaining := int64(b.N)
	perWorker := make([][]time.Duration, workers)
	var failures int64

	var wg sync.WaitGroup
	b.ResetTimer()
	start := time.Now()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			local := make([]time.Duration, 0, b.N/workers+1)
			for atomic.AddInt64(&remaining, -1) >= 0 {
				t0 := time.Now()
				err := g.establish(id.sshUser, id.signer)
				elapsed := time.Since(t0)
				if err != nil {
					atomic.AddInt64(&failures, 1)
					continue
				}
				local = append(local, elapsed)
			}
			perWorker[w] = local
		}(w)
	}
	wg.Wait()

	wall := time.Since(start)
	b.StopTimer()

	if n := atomic.LoadInt64(&failures); n > 0 {
		b.Fatalf("%d/%d session establishments failed under load", n, b.N)
	}

	all := make([]time.Duration, 0, b.N)
	for _, l := range perWorker {
		all = append(all, l...)
	}

	// Throughput is the headline number: authenticated sessions the gateway
	// completes per second at this concurrency.
	b.ReportMetric(float64(len(all))/wall.Seconds(), "sessions/s")
	b.ReportMetric(percentileMillis(all, 0.50), "p50-ms")
	b.ReportMetric(percentileMillis(all, 0.95), "p95-ms")
	b.ReportMetric(percentileMillis(all, 0.99), "p99-ms")
}

// percentileMillis returns the p-th percentile of d in milliseconds using
// nearest-rank, which needs no interpolation and is stable for small samples.
func percentileMillis(d []time.Duration, p float64) float64 {
	if len(d) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(d))
	copy(sorted, d)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	rank := int(p*float64(len(sorted))+0.5) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return float64(sorted[rank].Nanoseconds()) / 1e6
}

// -----------------------------------------------------------------------------
// Data plane: proxied session throughput
// -----------------------------------------------------------------------------

// benchChannel is a minimal ssh.Channel standing in for a real client or agent
// channel, so proxyConnection can be measured without the SSH transport's
// own encryption and windowing masking the gateway's per-byte overhead.
type benchChannel struct {
	r io.Reader
	w io.Writer
}

func (c *benchChannel) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *benchChannel) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *benchChannel) Close() error                { return nil }
func (c *benchChannel) CloseWrite() error           { return nil }
func (c *benchChannel) Stderr() io.ReadWriter       { return &benchChannel{r: eofReader{}, w: io.Discard} }

func (c *benchChannel) SendRequest(name string, wantReply bool, payload []byte) (bool, error) {
	return true, nil
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

// proxyPayloadSize is one op's worth of agent→client output. 256 KiB is large
// enough that per-byte costs dominate per-op setup, and is copied by io.Copy in
// 32 KiB chunks — so each op also exercises 8 recording writes.
const proxyPayloadSize = 256 * 1024

// recordingRotateEvery bounds the in-memory cast buffer: a SessionRecorder
// accumulates events until StopRecording flushes them, so a long run would
// otherwise grow without limit. Rotation happens with the timer stopped.
const recordingRotateEvery = 100

// BenchmarkGatewayProxyThroughput measures agent→client bytes per second
// through proxyConnection, with and without session recording in the path.
// The delta between the two is the cost the recording layer adds to every
// byte a user sees.
func BenchmarkGatewayProxyThroughput(b *testing.B) {
	payload := make([]byte, proxyPayloadSize)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}

	logger := benchLogger()
	srv := &Server{logger: logger, config: &common.Config{}}

	b.Run("proxy_only", func(b *testing.B) {
		b.SetBytes(proxyPayloadSize)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			srv.proxyConnection(
				&benchChannel{r: eofReader{}, w: io.Discard},
				&benchChannel{r: bytes.NewReader(payload), w: io.Discard},
				nil, true,
			)
		}
	})

	b.Run("recorded", func(b *testing.B) {
		recorder, err := recording.NewRecorder(b.TempDir(), logger)
		if err != nil {
			b.Fatalf("NewRecorder: %v", err)
		}

		session := 0
		sr := startBenchRecording(b, recorder, session)

		b.SetBytes(proxyPayloadSize)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if i > 0 && i%recordingRotateEvery == 0 {
				b.StopTimer()
				_ = recorder.StopRecording(benchSessionID(session))
				session++
				sr = startBenchRecording(b, recorder, session)
				b.StartTimer()
			}
			srv.proxyConnection(
				&benchChannel{r: eofReader{}, w: io.Discard},
				&benchChannel{r: bytes.NewReader(payload), w: io.Discard},
				sr, true,
			)
		}
		b.StopTimer()
		_ = recorder.StopRecording(benchSessionID(session))
	})
}

func benchSessionID(n int) string { return fmt.Sprintf("bench-session-%d", n) }

func startBenchRecording(b *testing.B, recorder *recording.Recorder, n int) *recording.SessionRecorder {
	b.Helper()
	sr, err := recorder.StartRecording(benchSessionID(n))
	if err != nil {
		b.Fatalf("StartRecording: %v", err)
	}
	return sr
}
