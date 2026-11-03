package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/orion-belt-dev/orion-belt/pkg/anomaly"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/metrics"
	"golang.org/x/crypto/ssh"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     checkWSOrigin,
}

// checkWSOrigin blocks cross-site WebSocket hijacking: the console's session
// cookie rides along on the upgrade, so accepting any Origin would let another
// page the victim visits open a root shell (/terminal/ws) as them. Allowed:
//   - no Origin header (osh and other non-browser clients);
//   - Origin host matching the request Host (the embedded /ui);
//   - a loopback Origin when the request also targets loopback — the Vite dev
//     server proxies /api with changeOrigin, so Host is rewritten to the
//     gateway while Origin stays http://localhost:5173.
func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return isLoopbackHost(u.Hostname()) && isLoopbackHost(hostOnly(r.Host))
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// TerminalBridge opens agent sessions for the web terminal / file browser.
type TerminalBridge interface {
	OpenAgentSession(machineID, remoteUser string) (ssh.Channel, <-chan *ssh.Request, error)
	ResolveMachine(name string) (*common.Machine, error)
}

func (s *APIServer) registerTerminalRoutes(protected *gin.RouterGroup) {
	protected.GET("/terminal/ws", s.terminalWS)
	protected.GET("/sessions/:id/watch", s.sessionWatchWS)
	protected.GET("/files/list", s.filesList)
	protected.GET("/files/download", s.filesDownload)
	protected.POST("/files/upload", s.filesUpload)
	protected.POST("/files/mkdir", s.filesMkdir)
	protected.DELETE("/files", s.filesDelete)
	protected.GET("/ssh-keys", s.listSSHKeys)
	protected.POST("/ssh-keys", s.addSSHKey)
	protected.DELETE("/ssh-keys/:id", s.deleteSSHKey)
}

func (s *APIServer) terminalWS(c *gin.Context) {
	if s.terminalBridge == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "terminal not available"})
		return
	}
	machine := c.Query("machine")
	remoteUser := c.Query("user")
	if remoteUser == "" {
		remoteUser = "root"
	}
	if machine == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "machine required"})
		return
	}

	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)
	m, err := s.terminalBridge.ResolveMachine(machine)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "machine not found"})
		return
	}
	if err := s.authService.CheckPermissionWithRemoteUser(c.Request.Context(), uid, m.ID, "ssh", remoteUser); err != nil {
		body := gin.H{"error": "permission denied"}
		if reason := s.denialReason(c, uid, m.ID, remoteUser); reason != "" {
			body["reason"] = reason
		}
		c.JSON(http.StatusForbidden, body)
		return
	}

	if s.recorder == nil {
		s.logger.Error("web terminal: recorder not configured; refusing session without audit trail")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session recording unavailable"})
		return
	}

	ctx := c.Request.Context()
	storagePath := s.recorder.GetRecordingStoragePath()
	session := common.NewSessionWithSource(uid, m.ID, remoteUser, storagePath, "web")
	if err := s.store.CreateSession(ctx, session); err != nil {
		s.logger.Error("Failed to create web terminal session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session record"})
		return
	}
	s.recordAudit(c, "session.web_terminal.start", "session:"+session.ID, map[string]interface{}{
		"machine_id":  m.ID,
		"machine":     m.Name,
		"remote_user": remoteUser,
		"source":      "web",
	})
	if flags, err := anomaly.Check(ctx, s.store, session, m, c.ClientIP()); err != nil {
		s.logger.Warn("Anomaly check failed for session %s: %v", session.ID, err)
	} else {
		for _, f := range flags {
			s.logger.Info("Session %s flagged %s: %s", session.ID, f.Kind, f.Signal)
		}
	}
	metrics.Default.SessionStarted()

	title := fmt.Sprintf("%s as %s (web)", m.Name, remoteUser)
	sessionRecorder, recErr := s.recorder.StartRecordingSized(session.ID, 120, 40, title)
	if recErr != nil {
		s.logger.Error("Failed to start web terminal recording: %v", recErr)
		_ = s.store.EndSession(context.Background(), session.ID, time.Now())
		metrics.Default.SessionEnded()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start session recording"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		_ = s.recorder.StopRecording(session.ID)
		_ = s.store.EndSession(context.Background(), session.ID, time.Now())
		metrics.Default.SessionEnded()
		return
	}
	defer conn.Close()
	defer func() {
		endTime := time.Now()
		bg := context.Background()
		_ = s.store.EndSession(bg, session.ID, endTime)
		_ = s.recorder.StopRecording(session.ID)
		metrics.Default.SessionEnded()
		entry := common.NewAuditLog(uid, "session.web_terminal.end", "session:"+session.ID, c.ClientIP(), map[string]interface{}{
			"machine_id": m.ID,
			"machine":    m.Name,
			"source":     "web",
		})
		_ = s.store.CreateAuditLog(bg, entry)
	}()

	channel, reqs, err := s.terminalBridge.OpenAgentSession(m.ID, remoteUser)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(reqs)

	ptyPayload := struct {
		Term     string
		Columns  uint32
		Rows     uint32
		Width    uint32
		Height   uint32
		Modelist string
	}{"xterm-256color", 120, 40, 0, 0, ""}
	_, _ = channel.SendRequest("pty-req", true, ssh.Marshal(&ptyPayload))
	_, _ = channel.SendRequest("shell", true, ssh.Marshal(&struct{ User string }{remoteUser}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		for {
			n, err := channel.Read(buf)
			if n > 0 {
				if sessionRecorder != nil {
					_ = sessionRecorder.Write(buf[:n])
				}
				_ = conn.WriteMessage(websocket.BinaryMessage, buf[:n])
			}
			if err != nil {
				// Agent side ended first — unblock the WS read loop.
				_ = conn.Close()
				return
			}
		}
	}()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.TextMessage && strings.Contains(string(data), `"type":"resize"`) {
			var r struct {
				Cols uint32 `json:"cols"`
				Rows uint32 `json:"rows"`
			}
			if json.Unmarshal(data, &r) == nil {
				_, _ = channel.SendRequest("window-change", false, ssh.Marshal(&struct {
					Columns, Rows, Width, Height uint32
				}{r.Cols, r.Rows, 0, 0}))
				if sessionRecorder != nil {
					_ = sessionRecorder.RecordResize(r.Cols, r.Rows)
				}
			}
			continue
		}
		// Output-only cast: do not record keystrokes (PTY echo would duplicate them).
		if _, err := channel.Write(data); err != nil {
			break
		}
	}
	// Client disconnect (or channel write failure): close the agent channel so
	// the agent→ws goroutine exits. Without this, <-done blocks forever while
	// the shell stays open, so EndSession/StopRecording never run.
	_ = channel.Close()
	<-done
}

func (s *APIServer) filesList(c *gin.Context) {
	machine := c.Query("machine")
	path := c.DefaultQuery("path", "/")
	remoteUser := c.DefaultQuery("user", "root")
	cmd := fmt.Sprintf(`python3 -c 'import os,json,sys; p=sys.argv[1];
entries=[]
try:
  for n in sorted(os.listdir(p)):
    fp=os.path.join(p,n); st=os.lstat(fp)
    entries.append({"name":n,"path":fp,"is_dir":os.path.isdir(fp),"size":st.st_size,"mtime":int(st.st_mtime)})
except Exception as e:
  print(json.dumps({"error":str(e)})); sys.exit(1)
print(json.dumps({"path":p,"entries":entries}))' %s`, shellQuote(path))
	res, err := s.execOnMachine(c, machine, remoteUser, nil, cmd)
	if err != nil {
		res, err = s.execOnMachine(c, machine, remoteUser, nil, fmt.Sprintf("ls -la %s", shellQuote(path)))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"path": path, "raw": string(res.out)})
		return
	}
	c.Data(http.StatusOK, "application/json", res.out)
}

func (s *APIServer) filesDownload(c *gin.Context) {
	machine := c.Query("machine")
	path := c.Query("path")
	remoteUser := c.DefaultQuery("user", "root")
	q := shellQuote(path)
	res, err := s.execOnMachine(c, machine, remoteUser, nil, fmt.Sprintf("base64 -w0 %s 2>/dev/null || base64 %s", q, q))
	if err == nil && res.status != 0 {
		err = res.failure()
	}
	if err != nil {
		s.auditFileOp(c, "files.download", machine, remoteUser, path, res, err, nil)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(res.out)))
	if err != nil {
		s.auditFileOp(c, "files.download", machine, remoteUser, path, res, errors.New("decode failed"), nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "decode failed"})
		return
	}
	s.auditFileOp(c, "files.download", machine, remoteUser, path, res, nil, contentMeta(raw))
	c.Header("Content-Disposition", "attachment; filename="+baseName(path))
	c.Data(http.StatusOK, "application/octet-stream", raw)
}

func (s *APIServer) filesUpload(c *gin.Context) {
	machine := c.PostForm("machine")
	path := c.PostForm("path")
	remoteUser := c.DefaultPostForm("user", "root")
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	defer f.Close()
	// The file goes to the remote cat on stdin, hashed on the way. It used to
	// be a base64 command-line argument, which failed past ~96 KB (ARG_MAX).
	sum := sha256.New()
	counted := &countingReader{r: io.TeeReader(f, sum)}
	res, err := s.execOnMachine(c, machine, remoteUser, counted, fmt.Sprintf("cat > %s", shellQuote(path)))
	if err == nil && res.status != 0 {
		err = res.failure()
	}
	s.auditFileOp(c, "files.upload", machine, remoteUser, path, res, err, map[string]interface{}{
		"size": counted.n, "sha256": hex.EncodeToString(sum.Sum(nil)),
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "uploaded", "path": path, "size": counted.n})
}

func (s *APIServer) filesMkdir(c *gin.Context) {
	var req struct {
		Machine string `json:"machine"`
		Path    string `json:"path"`
		User    string `json:"user"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.User == "" {
		req.User = "root"
	}
	res, err := s.execOnMachine(c, req.Machine, req.User, nil, fmt.Sprintf("mkdir -p %s", shellQuote(req.Path)))
	if err == nil && res.status != 0 {
		err = res.failure()
	}
	s.auditFileOp(c, "files.mkdir", req.Machine, req.User, req.Path, res, err, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "created"})
}

func (s *APIServer) filesDelete(c *gin.Context) {
	machine := c.Query("machine")
	path := c.Query("path")
	remoteUser := c.DefaultQuery("user", "root")
	res, err := s.execOnMachine(c, machine, remoteUser, nil, fmt.Sprintf("rm -rf %s", shellQuote(path)))
	if err == nil && res.status != 0 {
		err = res.failure()
	}
	s.auditFileOp(c, "files.delete", machine, remoteUser, path, res, err, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// execResult is what a command run on a machine for the file browser
// returned. status is the remote exit status, or -1 when the agent sent none.
type execResult struct {
	out       []byte
	status    int
	machineID string
}

func (r execResult) failure() error {
	return fmt.Errorf("command failed on the host (exit status %d)", r.status)
}

// errExecDenied is returned when the caller holds no ssh grant for the
// machine and remote user; the audit entry records it as denied.
var errExecDenied = errors.New("permission denied")

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// execOnMachine runs command on the machine as remoteUser, feeding it stdin
// (nil for none; EOF is sent either way) and collecting its output.
func (s *APIServer) execOnMachine(c *gin.Context, machineName, remoteUser string, stdin io.Reader, command string) (execResult, error) {
	res := execResult{status: -1}
	if s.terminalBridge == nil {
		return res, fmt.Errorf("bridge unavailable")
	}
	userID, _ := c.Get("user_id")
	m, err := s.terminalBridge.ResolveMachine(machineName)
	if err != nil {
		return res, fmt.Errorf("machine not found")
	}
	res.machineID = m.ID
	if err := s.authService.CheckPermissionWithRemoteUser(c.Request.Context(), userID.(string), m.ID, "ssh", remoteUser); err != nil {
		return res, errExecDenied
	}
	channel, reqs, err := s.terminalBridge.OpenAgentSession(m.ID, remoteUser)
	if err != nil {
		return res, err
	}
	defer channel.Close()

	// exit-status arrives as a channel request; reqs is closed once the
	// channel is, which is when the status is known to have been seen.
	statusDone := make(chan int, 1)
	go func() {
		status := -1
		for req := range reqs {
			if req.Type == "exit-status" && len(req.Payload) >= 4 {
				status = int(binary.BigEndian.Uint32(req.Payload))
			}
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
		statusDone <- status
	}()

	ok, err := channel.SendRequest("exec", true, ssh.Marshal(&struct{ Command, User string }{command, remoteUser}))
	if err != nil || !ok {
		return res, fmt.Errorf("exec rejected")
	}
	// Output is read while stdin is written, so a chatty command cannot
	// stall a large upload.
	var buf strings.Builder
	outDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, channel)
		close(outDone)
	}()
	var writeErr error
	if stdin != nil {
		_, writeErr = io.Copy(channel, stdin)
	}
	_ = channel.CloseWrite()
	<-outDone
	_ = channel.Close()
	res.out = []byte(buf.String())
	res.status = <-statusDone
	if writeErr != nil {
		return res, fmt.Errorf("sending data to the host: %w", writeErr)
	}
	return res, nil
}

// auditFileOp records a file browser action on a machine, including failed
// and refused attempts: these run outside a recorded session, so the audit
// log is their only record.
func (s *APIServer) auditFileOp(c *gin.Context, action, machine, remoteUser, path string, res execResult, opErr error, extra map[string]interface{}) {
	meta := map[string]interface{}{
		"machine":     machine,
		"remote_user": remoteUser,
		"path":        path,
		"outcome":     "ok",
	}
	if res.status >= 0 {
		meta["exit_status"] = res.status
	}
	switch {
	case errors.Is(opErr, errExecDenied):
		meta["outcome"] = "denied"
	case opErr != nil:
		meta["outcome"] = "failed"
		meta["error"] = opErr.Error()
	}
	for k, v := range extra {
		meta[k] = v
	}
	resource := "machine:" + res.machineID
	if res.machineID == "" {
		resource = "machine:" + machine
	}
	s.recordAudit(c, action, resource, meta)
}

// contentMeta identifies transferred content in an audit entry without
// storing it.
func contentMeta(b []byte) map[string]interface{} {
	sum := sha256.Sum256(b)
	return map[string]interface{}{"size": len(b), "sha256": hex.EncodeToString(sum[:])}
}

func (s *APIServer) listSSHKeys(c *gin.Context) {
	userID, _ := c.Get("user_id")
	keys, err := s.store.ListUserSSHKeys(c.Request.Context(), userID.(string))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, keys)
}

func (s *APIServer) addSSHKey(c *gin.Context) {
	userID, _ := c.Get("user_id")
	var req struct {
		Name      string `json:"name" binding:"required"`
		PublicKey string `json:"public_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid public key (supports sk-ssh-ed25519 / FIDO)"})
		return
	}
	key := &common.SSHKey{
		ID:        uuid.New().String(),
		UserID:    userID.(string),
		Name:      req.Name,
		PublicKey: strings.TrimSpace(req.PublicKey),
		KeyType:   pk.Type(),
		CreatedAt: time.Now(),
	}
	if err := s.store.CreateSSHKey(c.Request.Context(), key); err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, key)
}

// deleteSSHKey removes one of the caller's own keys. A key belonging to
// someone else reports not-found, the same as a missing ID, so the endpoint
// can neither strip another user's login keys nor confirm which IDs exist.
func (s *APIServer) deleteSSHKey(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	keys, err := s.store.ListUserSSHKeys(ctx, c.GetString("user_id"))
	if err != nil {
		s.fail(c, err)
		return
	}
	owned := false
	for _, k := range keys {
		if k.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err := s.store.DeleteSSHKey(ctx, id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
