package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// readHeaderTimeout bounds how long a client may take to send its
	// request headers. Without it one host can hold any number of
	// connections open by trickling headers (Slowloris), before any auth.
	readHeaderTimeout = 10 * time.Second
	// idleTimeout closes keep-alive connections that sit unused.
	idleTimeout = 120 * time.Second
	// There is no overall read or write timeout: uploads, report exports,
	// playback and the WebSocket terminal are legitimately long.

	// maxRequestBody caps a request body. The largest JSON the API takes
	// (a 500-pair bulk grant, a plugin config) is far below it.
	maxRequestBody = 4 << 20
	// maxUploadBody caps a file browser upload.
	maxUploadBody = 2 << 30
	uploadPath    = "/api/v1/files/upload"
)

// Start serves the API on addr until Shutdown. It returns nil after a
// Shutdown.
func (s *APIServer) Start(addr string) error {
	s.logger.Info("Starting API server on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	s.httpMu.Lock()
	if s.httpClosed {
		s.httpMu.Unlock()
		return nil
	}
	s.httpSrv = srv
	s.httpMu.Unlock()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting requests and waits for those in flight, until
// ctx expires. Hijacked WebSocket connections are not waited for.
func (s *APIServer) Shutdown(ctx context.Context) error {
	s.httpMu.Lock()
	s.httpClosed = true
	srv := s.httpSrv
	s.httpMu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// bodyLimitMiddleware caps request bodies, so an unauthenticated client
// cannot make the server buffer an arbitrarily large JSON body.
func bodyLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := int64(maxRequestBody)
		if c.Request.URL.Path == uploadPath {
			limit = maxUploadBody
		}
		if c.Request.ContentLength > limit {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// securityHeadersMiddleware sets the browser hardening headers on every
// response (SECURITY.md). The console is never meant to be framed. HSTS is
// sent only when the public URL is HTTPS, so a plain-HTTP lab keeps working.
// The CSP carries frame-ancestors only: a script-src policy needs a review
// of the console's inline code first.
func securityHeadersMiddleware(publicURL string) gin.HandlerFunc {
	hsts := false
	if u, err := url.Parse(strings.TrimSpace(publicURL)); err == nil && u.Scheme == "https" {
		hsts = true
	}
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
