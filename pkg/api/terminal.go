package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/metrics"
	"golang.org/x/crypto/ssh"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
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
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
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
	out, err := s.execOnMachine(c, machine, remoteUser, cmd)
	if err != nil {
		out, err = s.execOnMachine(c, machine, remoteUser, fmt.Sprintf("ls -la %s", shellQuote(path)))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"path": path, "raw": string(out)})
		return
	}
	c.Data(http.StatusOK, "application/json", out)
}

func (s *APIServer) filesDownload(c *gin.Context) {
	machine := c.Query("machine")
	path := c.Query("path")
	remoteUser := c.DefaultQuery("user", "root")
	q := shellQuote(path)
	out, err := s.execOnMachine(c, machine, remoteUser, fmt.Sprintf("base64 -w0 %s 2>/dev/null || base64 %s", q, q))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "decode failed"})
		return
	}
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
	data, err := io.ReadAll(f)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	cmd := fmt.Sprintf("echo %s | base64 -d > %s", shellQuote(b64), shellQuote(path))
	if _, err := s.execOnMachine(c, machine, remoteUser, cmd); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "uploaded", "path": path, "size": len(data)})
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
	if _, err := s.execOnMachine(c, req.Machine, req.User, fmt.Sprintf("mkdir -p %s", shellQuote(req.Path))); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "created"})
}

func (s *APIServer) filesDelete(c *gin.Context) {
	machine := c.Query("machine")
	path := c.Query("path")
	remoteUser := c.DefaultQuery("user", "root")
	if _, err := s.execOnMachine(c, machine, remoteUser, fmt.Sprintf("rm -rf %s", shellQuote(path))); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (s *APIServer) execOnMachine(c *gin.Context, machineName, remoteUser, command string) ([]byte, error) {
	if s.terminalBridge == nil {
		return nil, fmt.Errorf("bridge unavailable")
	}
	userID, _ := c.Get("user_id")
	m, err := s.terminalBridge.ResolveMachine(machineName)
	if err != nil {
		return nil, fmt.Errorf("machine not found")
	}
	if err := s.authService.CheckPermissionWithRemoteUser(c.Request.Context(), userID.(string), m.ID, "ssh", remoteUser); err != nil {
		return nil, fmt.Errorf("permission denied")
	}
	channel, reqs, err := s.terminalBridge.OpenAgentSession(m.ID, remoteUser)
	if err != nil {
		return nil, err
	}
	defer channel.Close()
	go ssh.DiscardRequests(reqs)

	ok, err := channel.SendRequest("exec", true, ssh.Marshal(&struct{ Command, User string }{command, remoteUser}))
	if err != nil || !ok {
		return nil, fmt.Errorf("exec rejected")
	}
	var buf strings.Builder
	_, _ = io.Copy(&buf, channel)
	return []byte(buf.String()), nil
}

func (s *APIServer) listSSHKeys(c *gin.Context) {
	userID, _ := c.Get("user_id")
	keys, err := s.store.ListUserSSHKeys(c.Request.Context(), userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, key)
}

func (s *APIServer) deleteSSHKey(c *gin.Context) {
	if err := s.store.DeleteSSHKey(c.Request.Context(), c.Param("id")); err != nil {
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
