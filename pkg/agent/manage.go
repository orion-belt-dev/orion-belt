package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// Remote management: reload, drain and update, driven by orion:* control
// commands from the gateway.

// defaultDrainTimeout is how long a drain waits for running sessions before
// closing them, unless the command names another.
const defaultDrainTimeout = 10 * time.Minute

// errDraining is what new work is refused with while the agent drains.
var errDraining = errors.New("agent is draining for maintenance; try again shortly")

// work tracks sessions, tunnels and kubectl relays in progress, so a drain
// can refuse new ones and wait for the rest.
type work struct {
	mu       sync.Mutex
	draining bool
	next     int
	active   map[int]io.Closer
	idle     chan struct{} // closed when draining and nothing is active
}

// begin registers c as running work and returns the function that ends it.
// It refuses while draining.
func (a *Agent) begin(c io.Closer) (end func(), err error) {
	w := &a.work
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.draining {
		return nil, errDraining
	}
	if w.active == nil {
		w.active = map[int]io.Closer{}
	}
	id := w.next
	w.next++
	w.active[id] = c
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.active, id)
		if w.draining && len(w.active) == 0 && w.idle != nil {
			close(w.idle)
			w.idle = nil
		}
	}, nil
}

// drain stops new work and returns a channel closed once running work has
// finished, or timeout has passed and what was left was closed.
func (a *Agent) drain(timeout time.Duration) <-chan struct{} {
	w := &a.work
	w.mu.Lock()
	w.draining = true
	done := make(chan struct{})
	if len(w.active) == 0 {
		w.mu.Unlock()
		close(done)
		return done
	}
	idle := make(chan struct{})
	w.idle = idle
	w.mu.Unlock()

	go func() {
		defer close(done)
		select {
		case <-idle:
		case <-time.After(timeout):
			w.mu.Lock()
			left := make([]io.Closer, 0, len(w.active))
			for _, c := range w.active {
				left = append(left, c)
			}
			w.mu.Unlock()
			a.logger.Warn("Drain timeout: closing %d remaining sessions", len(left))
			for _, c := range left {
				c.Close()
			}
		}
	}()
	return done
}

// undrain accepts new work again.
func (a *Agent) undrain() {
	w := &a.work
	w.mu.Lock()
	defer w.mu.Unlock()
	w.draining = false
	w.idle = nil
}

// workStatus reports whether the agent drains and how much work runs.
func (a *Agent) workStatus() (draining bool, active int) {
	a.work.mu.Lock()
	defer a.work.mu.Unlock()
	return a.work.draining, len(a.work.active)
}

// --- reload -----------------------------------------------------------------

// SetConfigPath records the file the agent was started from, which reload
// reads again.
func (a *Agent) SetConfigPath(path string) { a.configPath = path }

// agentConfig returns the agent section of the config, which reload replaces.
func (a *Agent) agentConfig() common.AgentConfig {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.config.Agent
}

// reload reads the config file again and applies what can change while
// connected: tags, tunnel addresses and Kubernetes connection settings.
// Fields that need a new connection or identity are reported, not applied.
// An unreadable or invalid file leaves the running config untouched.
func (a *Agent) reload() (applied, restartRequired []string, err error) {
	if a.configPath == "" {
		return nil, nil, errors.New("config path unknown; restart the agent instead")
	}
	next, err := common.LoadConfig(a.configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", a.configPath, err)
	}

	a.cfgMu.Lock()
	cur := a.config
	if next.Server.Host != cur.Server.Host || next.Server.Port != cur.Server.Port {
		restartRequired = append(restartRequired, "server")
	}
	na, ca := next.Auth, cur.Auth
	if na.KeyFile != ca.KeyFile || na.KnownHosts != ca.KnownHosts ||
		na.StrictHostKeyChecking != ca.StrictHostKeyChecking || na.HostCAPublicKey != ca.HostCAPublicKey {
		restartRequired = append(restartRequired, "auth")
	}
	if next.Agent.Name != cur.Agent.Name {
		restartRequired = append(restartRequired, "agent.name")
	}
	if next.Agent.Kubernetes.Enabled != cur.Agent.Kubernetes.Enabled {
		restartRequired = append(restartRequired, "agent.kubernetes.enabled")
	}
	if !mapsEqual(next.Agent.Tags, cur.Agent.Tags) {
		cur.Agent.Tags = next.Agent.Tags
		applied = append(applied, "agent.tags")
	}
	if !mapsEqual(next.Agent.Tunnels, cur.Agent.Tunnels) {
		cur.Agent.Tunnels = next.Agent.Tunnels
		applied = append(applied, "agent.tunnels")
	}
	kubeChanged := next.Agent.Kubernetes != cur.Agent.Kubernetes && next.Agent.Kubernetes.Enabled == cur.Agent.Kubernetes.Enabled
	if kubeChanged {
		cur.Agent.Kubernetes = next.Agent.Kubernetes
		applied = append(applied, "agent.kubernetes")
	}
	if !boolPtrEqual(next.Agent.RemoteUpdate, cur.Agent.RemoteUpdate) {
		cur.Agent.RemoteUpdate = next.Agent.RemoteUpdate
		applied = append(applied, "agent.remote_update")
	}
	a.cfgMu.Unlock()

	if kubeChanged {
		a.resetKubeClient() // the next use connects with the new settings
	}
	return applied, restartRequired, nil
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// --- update -----------------------------------------------------------------

// Seams for tests: where the running binary is, how it is downloaded, and
// how big a download may be.
var (
	agentExecutable = func() (string, error) {
		p, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(p)
	}
	updateHTTPClient = &http.Client{Timeout: 5 * time.Minute}
	maxUpdateBytes   = int64(512 << 20)
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// updateRequest is a parsed orion:update command.
type updateRequest struct {
	URL          string
	SHA256       string
	DrainTimeout time.Duration
}

// parseUpdate reads "orion:update <https-url> <sha256> [drain-seconds]".
func parseUpdate(fields []string) (updateRequest, error) {
	if len(fields) < 3 || len(fields) > 4 {
		return updateRequest{}, errors.New("usage: orion:update <https-url> <sha256> [drain-seconds]")
	}
	u, err := url.Parse(fields[1])
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return updateRequest{}, errors.New("update URL must be https")
	}
	sum := strings.ToLower(fields[2])
	if !sha256Hex.MatchString(sum) {
		return updateRequest{}, errors.New("sha256 must be 64 hex characters")
	}
	req := updateRequest{URL: u.String(), SHA256: sum, DrainTimeout: defaultDrainTimeout}
	if len(fields) == 4 {
		secs, err := strconv.Atoi(fields[3])
		if err != nil || secs < 0 {
			return updateRequest{}, errors.New("drain-seconds must be a non-negative integer")
		}
		req.DrainTimeout = time.Duration(secs) * time.Second
	}
	return req, nil
}

// stageUpdate downloads the binary, checks its SHA-256 before anything is
// written, and writes it next to the running executable as <exe>.new. A
// .tar.gz release archive is accepted; its orion-belt-agent entry is used.
func stageUpdate(ctx context.Context, req updateRequest) (staged, exe string, err error) {
	exe, err = agentExecutable()
	if err != nil {
		return "", "", fmt.Errorf("locate agent binary: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := updateHTTPClient.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxUpdateBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("download: %w", err)
	}
	if int64(len(data)) > maxUpdateBytes {
		return "", "", fmt.Errorf("download larger than %d bytes", maxUpdateBytes)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != req.SHA256 {
		return "", "", fmt.Errorf("sha256 mismatch: got %s, want %s", got, req.SHA256)
	}
	bin := data
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		if bin, err = agentFromArchive(data); err != nil {
			return "", "", err
		}
	}

	staged = exe + ".new"
	f, err := os.OpenFile(staged, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", "", fmt.Errorf("stage update: %w", err)
	}
	if _, err := f.Write(bin); err != nil {
		f.Close()
		os.Remove(staged)
		return "", "", fmt.Errorf("stage update: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(staged)
		return "", "", fmt.Errorf("stage update: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(staged)
		return "", "", err
	}
	return staged, exe, nil
}

// agentEntry matches the binary in a release archive: at the root, or in
// one top-level folder. Release archives also ship an init script named
// packaging/sysv/orion-belt-agent, which must never be taken for it.
var agentEntry = regexp.MustCompile(`^(\./)?([^/]+/)?orion-belt-agent$`)

// agentFromArchive returns the orion-belt-agent binary inside a .tar.gz.
func agentFromArchive(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("archive has no orion-belt-agent binary")
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg && agentEntry.MatchString(h.Name) {
			return io.ReadAll(io.LimitReader(tr, maxUpdateBytes))
		}
	}
}

// installUpdate drains, swaps the staged binary into place (keeping the old
// one as <exe>.old) and restarts.
func (a *Agent) installUpdate(staged, exe string, drainTimeout time.Duration) {
	<-a.drain(drainTimeout)
	_ = os.Remove(exe + ".old")
	if err := os.Link(exe, exe+".old"); err != nil {
		a.logger.Warn("Update: could not keep the previous binary: %v", err)
	}
	if err := os.Rename(staged, exe); err != nil {
		a.logger.Error("Update: install failed, staying on the current binary: %v", err)
		os.Remove(staged)
		a.undrain()
		return
	}
	a.logger.Info("Update installed; restarting")
	restartAgent(a)
}

// remoteUpdateAllowed reports whether the config lets the gateway update
// this agent. It is on unless agent.remote_update is false.
func (a *Agent) remoteUpdateAllowed() bool {
	v := a.agentConfig().RemoteUpdate
	return v == nil || *v
}

// manageCommand runs reload, drain, undrain and update. ok is false when
// cmd is not one of them.
func (a *Agent) manageCommand(cmd string) (result map[string]interface{}, verb string, ok bool) {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return nil, "", false
	}
	verb = fields[0]
	fail := func(err error) map[string]interface{} {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	switch verb {
	case "orion:reload":
		applied, restart, err := a.reload()
		if err != nil {
			return fail(err), verb, true
		}
		return map[string]interface{}{"ok": true, "applied": nonNil(applied), "restart_required": nonNil(restart)}, verb, true

	case "orion:drain":
		timeout := defaultDrainTimeout
		if len(fields) == 2 {
			secs, err := strconv.Atoi(fields[1])
			if err != nil || secs < 0 {
				return fail(errors.New("usage: orion:drain [timeout-seconds]")), verb, true
			}
			timeout = time.Duration(secs) * time.Second
		} else if len(fields) > 2 {
			return fail(errors.New("usage: orion:drain [timeout-seconds]")), verb, true
		}
		a.drain(timeout)
		_, active := a.workStatus()
		return map[string]interface{}{"ok": true, "draining": true, "active": active, "timeout_seconds": int(timeout.Seconds())}, verb, true

	case "orion:undrain":
		a.undrain()
		return map[string]interface{}{"ok": true, "draining": false}, verb, true

	case "orion:update":
		if !a.remoteUpdateAllowed() {
			return fail(errors.New("remote update is disabled on this agent (agent.remote_update: false)")), verb, true
		}
		req, err := parseUpdate(fields)
		if err != nil {
			return fail(err), verb, true
		}
		ctx, cancel := context.WithTimeout(context.Background(), updateHTTPClient.Timeout)
		defer cancel()
		staged, exe, err := stageUpdate(ctx, req)
		if err != nil {
			return fail(err), verb, true
		}
		go a.installUpdate(staged, exe, req.DrainTimeout)
		return map[string]interface{}{
			"ok": true, "staged": staged, "sha256": req.SHA256,
			"message": "verified and staged; installing after sessions drain, then restarting",
		}, verb, true
	}
	return nil, "", false
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}
