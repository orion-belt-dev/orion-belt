package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// registerSSHCARoutes wires the SSH Certificate Authority endpoints: cert
// issuance/CA-trust lookup for any authenticated user (protected), and
// lifecycle management (export, list, revoke) for admins/operators.
func (s *APIServer) registerSSHCARoutes(protected, admin *gin.RouterGroup) {
	protected.GET("/ssh-cert/ca", s.getTrustedCA)
	protected.POST("/ssh-cert", s.issueUserCert)

	admin.GET("/ca/export", s.exportCA)
	admin.GET("/ssh-certificates", s.listSSHCertificates)
	admin.POST("/ssh-certificates/:serial/revoke", s.revokeSSHCertificate)
}

// getTrustedCA lets any authenticated client discover whether SSH CA is
// enabled and, if so, fetch the CA public keys it needs to trust — this is
// what makes client-side cert usage auto-detect server capability instead
// of requiring a local opt-in flag (osh/ocp/oadmin call this once and cache
// the result alongside their signer).
func (s *APIServer) getTrustedCA(c *gin.Context) {
	s.writeCAExport(c)
}

// exportCA returns the CA public keys an operator distributes out-of-band
// to clients and agents that can't (or shouldn't) call getTrustedCA
// themselves. Same payload as getTrustedCA; kept as a distinct admin-gated
// route so CA trust material has an obvious, documented export path
// independent of the auto-detection endpoint.
func (s *APIServer) exportCA(c *gin.Context) {
	s.writeCAExport(c)
}

func (s *APIServer) writeCAExport(c *gin.Context) {
	if s.ca == nil {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	userCA, hostCA := s.ca.ExportPublicKeys()
	c.JSON(http.StatusOK, gin.H{
		"enabled": true,
		"user_ca": userCA,
		"host_ca": hostCA,
	})
}

// IssueUserCertRequest is the body for POST /ssh-cert.
type IssueUserCertRequest struct {
	PublicKey string `json:"public_key" binding:"required"` // authorized_keys-format line
	TTLHours  int    `json:"ttl_hours,omitempty"`           // 0 = server default
}

// issueUserCert signs a short-lived SSH user certificate for the calling
// (already-authenticated) user. Authentication for this endpoint goes
// through the standard protected-route auth middleware, which already
// enforces MFA where required (enforceMFAAfterPubkey at login time) — no
// separate MFA check is needed here.
func (s *APIServer) issueUserCert(c *gin.Context) {
	if s.ca == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ssh certificate authority not enabled", "enabled": false})
		return
	}
	var req IssueUserCertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid public_key: " + err.Error()})
		return
	}

	userID := c.GetString("user_id")
	username := c.GetString("username")
	if username == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "authenticated user has no username"})
		return
	}

	cert, err := s.ca.IssueUserCert(c.Request.Context(), userID, username, pub, time.Duration(req.TTLHours)*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"certificate": string(ssh.MarshalAuthorizedKey(cert)),
		"serial":      strconv.FormatUint(cert.Serial, 10),
		"expires_at":  time.Unix(int64(cert.ValidBefore), 0).UTC(),
	})
}

// listSSHCertificates returns issued-certificate lifecycle records
// (serial, subject, principals, issued/expiry, revocation state) for the
// admin UI's Certificate Authority panel.
func (s *APIServer) listSSHCertificates(c *gin.Context) {
	if s.ca == nil {
		c.JSON(http.StatusOK, gin.H{"certificates": []interface{}{}})
		return
	}

	limit := 100
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v >= 0 {
		offset = v
	}

	filter := common.SSHCertFilter{
		CertType:  c.Query("cert_type"),
		SubjectID: c.Query("subject_id"),
	}
	if v := c.Query("active"); v != "" {
		active := v == "true"
		filter.Active = &active
	}

	certs, err := s.store.ListSSHCertificates(c.Request.Context(), filter, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list certificates"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"certificates": certs})
}

// RevokeCertRequest is the body for POST /admin/ssh-certificates/:serial/revoke.
type RevokeCertRequest struct {
	Reason string `json:"reason"`
}

// revokeSSHCertificate revokes a certificate ahead of its TTL expiry. The
// in-memory revocation cache is updated immediately (see
// ca.Authority.RevokeCertificate) so this takes effect on this process
// right away; other server instances pick it up on their next
// runCARevocationRefreshLoop tick.
func (s *APIServer) revokeSSHCertificate(c *gin.Context) {
	if s.ca == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ssh certificate authority not enabled"})
		return
	}
	serial := c.Param("serial")
	var req RevokeCertRequest
	_ = c.ShouldBindJSON(&req) // reason is optional

	revokedBy := c.GetString("user_id")
	if err := s.ca.RevokeCertificate(c.Request.Context(), serial, revokedBy, req.Reason); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	s.recordAudit(c, "ssh_cert.revoke", "ssh_certificate:"+serial, map[string]interface{}{"reason": req.Reason})
	c.JSON(http.StatusOK, gin.H{"status": "revoked", "serial": serial})
}
