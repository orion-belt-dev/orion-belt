package api

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, nil) // public URL https://gw.example.com
	r := e.do("GET", "/api/v1/version", "", nil)
	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Content-Security-Policy":   "frame-ancestors 'none'",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for h, v := range want {
		if got := r.hdr.Get(h); got != v {
			t.Errorf("%s = %q, want %q", h, got, v)
		}
	}
	if !strings.Contains(r.hdr.Get("Permissions-Policy"), "camera=()") {
		t.Errorf("Permissions-Policy = %q", r.hdr.Get("Permissions-Policy"))
	}
	// Errors carry them too.
	if e.do("GET", "/api/v1/no-such-route", "", nil).hdr.Get("X-Frame-Options") != "DENY" {
		t.Error("404 without X-Frame-Options")
	}

	plain := newEnv(t, func(o *Options) { o.Server.PublicURL = "http://10.0.0.5:8080" })
	if got := plain.do("GET", "/api/v1/version", "", nil).hdr.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS over plain HTTP = %q", got)
	}
}

func TestBodyLimit(t *testing.T) {
	e := newEnv(t, nil)
	big := strings.Repeat("x", maxRequestBody+1)

	// Declared too large: refused before the handler reads anything.
	r := e.do("POST", "/api/v1/public/login", "", `{"username":"`+big+`"}`)
	e.expect(r, http.StatusRequestEntityTooLarge)

	// Streamed without a length: the reader stops at the cap.
	req := httptest.NewRequest("POST", "/api/v1/public/login", io.MultiReader(strings.NewReader(`{"username":"`), strings.NewReader(big), strings.NewReader(`"}`)))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.srv.Router().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("streamed oversized body = %d, want 400", w.Code)
	}

	// Uploads have their own, larger cap.
	e.grant(e.alice, "root")
	e.withAgentBridge(func(string, []byte) (string, uint32) { return "", 0 })
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("machine", "web-01")
	_ = mw.WriteField("path", "/tmp/big")
	fw, _ := mw.CreateFormFile("file", "big")
	_, _ = fw.Write(bytes.Repeat([]byte{1}, maxRequestBody+1))
	_ = mw.Close()
	e.expect(e.do("POST", "/api/v1/files/upload", e.aliceKey, body.String(), "Content-Type", mw.FormDataContentType()), 200)
}

func TestStartShutdown(t *testing.T) {
	e := newEnv(t, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	done := make(chan error, 1)
	go func() { done <- e.srv.Start(addr) }()
	var resp *http.Response
	for deadline := time.Now().Add(5 * time.Second); ; {
		if resp, err = http.Get("http://" + addr + "/api/v1/version"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never answered: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp.Body.Close()

	e.srv.httpMu.Lock()
	srv := e.srv.httpSrv
	e.srv.httpMu.Unlock()
	if srv.ReadHeaderTimeout != readHeaderTimeout || srv.IdleTimeout != idleTimeout {
		t.Errorf("timeouts = %v / %v", srv.ReadHeaderTimeout, srv.IdleTimeout)
	}

	if err := e.srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start after Shutdown = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Shutdown")
	}
	// Shutdown before Start: Start returns at once.
	if err := e.srv.Start(addr); err != nil {
		t.Errorf("Start after Shutdown = %v", err)
	}
}
