package common

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mustUUID(t *testing.T, id string) {
	t.Helper()
	if _, err := uuid.Parse(id); err != nil {
		t.Errorf("ID %q is not a UUID: %v", id, err)
	}
}

func TestUser_HasPassword(t *testing.T) {
	var nilUser *User
	if nilUser.HasPassword() {
		t.Error("nil user reports a password")
	}
	if (&User{}).HasPassword() {
		t.Error("user without hash reports a password")
	}
	if !(&User{PasswordHash: "$argon2id$..."}).HasPassword() {
		t.Error("user with hash reports no password")
	}
}

func TestUser_EffectiveRole(t *testing.T) {
	cases := []struct {
		name    string
		role    string
		isAdmin bool
		want    string
	}{
		{"explicit admin", RoleAdmin, false, RoleAdmin},
		{"explicit operator", RoleOperator, false, RoleOperator},
		{"explicit auditor", RoleAuditor, false, RoleAuditor},
		{"legacy is_admin with stale user role", RoleUser, true, RoleAdmin},
		{"legacy is_admin with no role", "", true, RoleAdmin},
		{"plain user", RoleUser, false, RoleUser},
		{"unknown role kept", "contractor", false, "contractor"},
		{"no role defaults to user", "", false, RoleUser},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &User{Role: tc.role, IsAdmin: tc.isAdmin}
			if got := u.EffectiveRole(); got != tc.want {
				t.Errorf("EffectiveRole() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUser_HasRole(t *testing.T) {
	cases := []struct {
		role, min string
		want      bool
	}{
		{RoleAdmin, RoleOperator, true},
		{RoleOperator, RoleOperator, true},
		{RoleAuditor, RoleOperator, false},
		{RoleUser, RoleAuditor, false},
		{RoleUser, RoleUser, true},
		{"contractor", RoleUser, false}, // unknown roles rank below user
	}
	for _, tc := range cases {
		if got := (&User{Role: tc.role}).HasRole(tc.min); got != tc.want {
			t.Errorf("User{Role:%q}.HasRole(%q) = %v, want %v", tc.role, tc.min, got, tc.want)
		}
	}
}

func TestNewUser(t *testing.T) {
	admin := NewUser("root", "r@example.com", "ssh-ed25519 AAA", true)
	mustUUID(t, admin.ID)
	if admin.Role != RoleAdmin || !admin.IsAdmin || admin.Username != "root" || admin.Email != "r@example.com" || admin.PublicKey != "ssh-ed25519 AAA" {
		t.Errorf("NewUser(admin) = %+v", admin)
	}
	if admin.CreatedAt.IsZero() || !admin.CreatedAt.Equal(admin.UpdatedAt) {
		t.Errorf("timestamps created=%v updated=%v, want equal and set", admin.CreatedAt, admin.UpdatedAt)
	}
	if u := NewUser("bob", "", "", false); u.Role != RoleUser || u.IsAdmin {
		t.Errorf("NewUser(non-admin) role=%q isAdmin=%v", u.Role, u.IsAdmin)
	}
	if NewUser("a", "", "", false).ID == NewUser("a", "", "", false).ID {
		t.Error("two users share an ID")
	}
}

func TestNewMachine(t *testing.T) {
	m := NewMachine("web-01", "10.0.0.5", 22, map[string]string{"env": "prod"})
	mustUUID(t, m.ID)
	if m.Name != "web-01" || m.Hostname != "10.0.0.5" || m.Port != 22 || m.Tags["env"] != "prod" {
		t.Errorf("NewMachine = %+v", m)
	}
	if m.IsActive {
		t.Error("new machine is active before its agent connects")
	}
}

func TestNewCAKeyAndCertificate(t *testing.T) {
	k := NewCAKey(CATypeHost, "ed25519", "ssh-ed25519 AAA", "SHA256:x")
	mustUUID(t, k.ID)
	if !k.Active || k.CAType != CATypeHost || k.KeyAlgo != "ed25519" || k.Fingerprint != "SHA256:x" || k.CreatedAt.IsZero() {
		t.Errorf("NewCAKey = %+v", k)
	}

	issued := time.Now()
	c := NewSSHCertificate("42", CATypeUser, "u-1", "alice", []string{"alice"}, "SHA256:y", issued, issued.Add(time.Hour))
	mustUUID(t, c.ID)
	if c.Serial != "42" || c.SubjectID != "u-1" || c.KeyID != "alice" || c.PublicKeyFingerprint != "SHA256:y" || !c.ExpiresAt.Equal(issued.Add(time.Hour)) {
		t.Errorf("NewSSHCertificate = %+v", c)
	}
	if c.RevokedAt != nil {
		t.Error("new certificate is already revoked")
	}
}

func TestNewSession(t *testing.T) {
	s := NewSession("u", "m", "root", "/var/rec")
	mustUUID(t, s.ID)
	if s.Source != "ssh" || s.Status != "active" || s.RemoteUser != "root" {
		t.Errorf("NewSession = %+v", s)
	}
	if want := filepath.Join("/var/rec", s.ID+".cast"); s.RecordingPath != want {
		t.Errorf("RecordingPath = %q, want %q", s.RecordingPath, want)
	}

	if web := NewSessionWithSource("u", "m", "root", "/r", "web"); web.Source != "web" {
		t.Errorf("Source = %q, want web", web.Source)
	}
	if blank := NewSessionWithSource("u", "m", "root", "/r", ""); blank.Source != "ssh" {
		t.Errorf("blank Source = %q, want ssh default", blank.Source)
	}
}

func TestNewAccessRequestAndPermission(t *testing.T) {
	r := NewAccessRequest("u", "m", []string{"root", "deploy"}, "incident", 60)
	mustUUID(t, r.ID)
	if r.Status != "pending" || r.AccessType != "both" || r.Duration != 60 || len(r.RemoteUsers) != 2 || r.Reason != "incident" {
		t.Errorf("NewAccessRequest = %+v", r)
	}

	exp := time.Now().Add(time.Hour)
	p := NewPermission("u", "m", "ssh", []string{"root"}, "admin", &exp)
	mustUUID(t, p.ID)
	if p.AccessType != "ssh" || p.GrantedBy != "admin" || p.ExpiresAt != &exp || p.GrantedAt.IsZero() {
		t.Errorf("NewPermission = %+v", p)
	}
}

func TestNewAuditLogNotificationAPIKeyAndHTTPSession(t *testing.T) {
	meta := map[string]interface{}{"k": "v"}

	a := NewAuditLog("u", "login", "session", "10.0.0.1", meta)
	mustUUID(t, a.ID)
	if a.Action != "login" || a.Resource != "session" || a.IPAddress != "10.0.0.1" || a.Metadata["k"] != "v" || a.Timestamp.IsZero() {
		t.Errorf("NewAuditLog = %+v", a)
	}

	n := NewNotification("u", "access_request", "Title", "Body", meta)
	mustUUID(t, n.ID)
	if n.Type != "access_request" || n.Title != "Title" || n.Body != "Body" || n.ReadAt != nil {
		t.Errorf("NewNotification = %+v", n)
	}

	k := NewAPIKey("u", "ci", "hash", "ob_12345", nil)
	mustUUID(t, k.ID)
	if k.Name != "ci" || k.KeyHash != "hash" || k.KeyPrefix != "ob_12345" || k.ExpiresAt != nil || k.RevokedAt != nil {
		t.Errorf("NewAPIKey = %+v", k)
	}

	exp := time.Now().Add(time.Hour)
	s := NewHTTPSession("u", "tok", "10.0.0.1", "curl", exp)
	mustUUID(t, s.ID)
	if s.Token != "tok" || s.UserAgent != "curl" || !s.ExpiresAt.Equal(exp) || !s.CreatedAt.Equal(s.LastSeenAt) {
		t.Errorf("NewHTTPSession = %+v", s)
	}
}
