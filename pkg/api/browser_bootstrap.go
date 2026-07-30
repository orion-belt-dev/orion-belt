package api

import (
	"crypto/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

const (
	bootstrapCodeTTL   = 5 * time.Minute
	bootstrapCodeChars = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ" // Crockford-style, no 0/O/1/I/L
	bootstrapCodeLen   = 10                                 // ~49 bits of entropy
)

// bootstrapEntry is a single-use, short-lived code binding a browser
// session bootstrap to the CLI-authenticated user who requested it.
type bootstrapEntry struct {
	userID    string
	expiresAt time.Time
}

// bootstrapStore issues and redeems browser-bootstrap codes. This is the
// mechanism that replaced the web console's old "paste your SSH public
// key" login form (see docs/SSH_CA.md): a browser cannot prove possession
// of an arbitrary SSH private key the way a CLI holding the key file can,
// so instead the already-authenticated CLI (osh login) vouches for a new
// browser session by minting a short code the user redeems in-browser.
// In-memory, single-process — same tradeoff as challengeStore/rateLimiter.
type bootstrapStore struct {
	mu    sync.Mutex
	codes map[string]bootstrapEntry
}

func newBootstrapStore() *bootstrapStore {
	return &bootstrapStore{codes: make(map[string]bootstrapEntry)}
}

func (b *bootstrapStore) Issue(userID string) (string, time.Time, error) {
	buf := make([]byte, bootstrapCodeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	code := make([]byte, bootstrapCodeLen)
	for i, v := range buf {
		code[i] = bootstrapCodeChars[int(v)%len(bootstrapCodeChars)]
	}

	expiresAt := time.Now().Add(bootstrapCodeTTL)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gc()
	b.codes[string(code)] = bootstrapEntry{userID: userID, expiresAt: expiresAt}
	return string(code), expiresAt, nil
}

// Redeem consumes a code (single-use regardless of outcome) and returns
// the user it was issued for, if valid and unexpired.
func (b *bootstrapStore) Redeem(code string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.codes[code]
	if !ok {
		return "", false
	}
	delete(b.codes, code)
	if time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.userID, true
}

func (b *bootstrapStore) gc() {
	now := time.Now()
	for k, v := range b.codes {
		if now.After(v.expiresAt) {
			delete(b.codes, k)
		}
	}
}

// issueBrowserBootstrap is POST /auth/browser-bootstrap (protected — the
// caller already authenticated as a real CLI holding a real SSH key).
func (s *APIServer) issueBrowserBootstrap(c *gin.Context) {
	userID := c.GetString("user_id")
	code, expiresAt, err := s.bootstrap.Issue(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue bootstrap code"})
		return
	}
	s.recordAudit(c, "auth.browser_bootstrap.issue", "user:"+userID, nil)
	c.JSON(http.StatusOK, gin.H{"code": code, "expires_at": expiresAt})
}

// redeemBrowserBootstrap is POST /public/auth/browser-bootstrap/redeem —
// unauthenticated by definition (that's the point: it's how the browser
// gets its first credential), so it's rate-limited per IP on top of the
// code's own short TTL and single-use, non-guessable (~49 bit) design.
func (s *APIServer) redeemBrowserBootstrap(c *gin.Context) {
	if !s.rateLimiter.allow("bootstrap-redeem:" + c.ClientIP()) {
		c.Header("Retry-After", "60")
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, ok := s.bootstrap.Redeem(req.Code)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired code"})
		return
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired code"})
		return
	}

	session, rawSession, err := s.authService.CreateSession(ctx, user.ID, c.ClientIP(), c.Request.UserAgent(), 60*time.Minute)
	if err != nil {
		s.logger.Error("Failed to create session from browser bootstrap: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	response := LoginResponse{
		SessionToken: rawSession,
		ExpiresAt:    session.ExpiresAt,
	}
	response.User.ID = user.ID
	response.User.Username = user.Username
	response.User.Email = user.Email
	response.User.IsAdmin = user.IsAdmin || user.EffectiveRole() == common.RoleAdmin
	response.User.Role = user.EffectiveRole()
	response.User.MFAEnabled = user.MFAEnabled
	response.User.PasswordSet = user.HasPassword()
	response.User.MustSetPassword = !user.HasPassword()

	if s.jwt != nil && s.jwt.Enabled() {
		if token, exp, err := s.jwt.Issue(user.ID, user.Username, response.User.IsAdmin); err == nil {
			response.AccessToken = token
			response.ExpiresAt = exp
		}
	}

	_ = s.store.CreateAuditLog(ctx, common.NewAuditLog(user.ID, "auth.browser_bootstrap.redeem", "user:"+user.ID, c.ClientIP(), nil))

	c.JSON(http.StatusOK, response)
}
