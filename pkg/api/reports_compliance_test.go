package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

var compNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func compNames() *complianceNames {
	admin := &common.User{ID: "u-admin", Username: "admin", Role: common.RoleAdmin, IsAdmin: true}
	return &complianceNames{
		users: map[string]*common.User{
			"u-alice": {ID: "u-alice", Username: "alice", Role: common.RoleUser},
			"u-bob":   {ID: "u-bob", Username: "bob", Role: common.RoleUser},
			"u-admin": admin,
		},
		machines: map[string]*common.Machine{
			"m-web": {ID: "m-web", Name: "web-01", Tags: map[string]string{"env": "prod"}},
			"m-pay": {ID: "m-pay", Name: "pay-01", Tags: map[string]string{"pci": "in-scope"}},
		},
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
func ptrStr(s string) *string        { return &s }

// Every session row names who, what, when, why, and the approver — taken from
// the approved request that covered its start.
func TestBuildSOC2RowsTiesSessionsToTheirApproval(t *testing.T) {
	reviewed := compNow.Add(-2 * time.Hour)
	rows := buildSOC2Rows(soc2Inputs{
		From: compNow.Add(-24 * time.Hour), To: compNow,
		Names: compNames(),
		Requests: []*common.AccessRequest{{
			ID: "r1", UserID: "u-alice", MachineID: "m-web", RemoteUsers: []string{"root"},
			Reason: "incident 42", Status: "approved", RequestedAt: compNow.Add(-3 * time.Hour),
			ReviewedAt: &reviewed, ReviewedBy: ptrStr("u-admin"), ExpiresAt: ptrTime(compNow.Add(time.Hour)),
		}},
		Sessions: []*common.Session{{
			ID: "s1", UserID: "u-alice", MachineID: "m-web", RemoteUser: "root", Source: "ssh",
			Status: "completed", StartTime: compNow.Add(-time.Hour),
		}},
	})
	if len(rows) != 2 {
		t.Fatalf("want the request and the session, got %+v", rows)
	}
	req, sess := rows[0], rows[1]
	if req.EventType != "access_request" || req.Reason != "incident 42" || req.Approver != "admin" {
		t.Errorf("request row = %+v", req)
	}
	if sess.EventType != "session" || sess.Actor != "alice" || sess.Target != "web-01" {
		t.Errorf("session identity = %+v", sess)
	}
	if sess.Authorization != authzAccessRequest || sess.AuthorizedBy != "access_request:r1" ||
		sess.Reason != "incident 42" || sess.Approver != "admin" {
		t.Errorf("session authorisation = %+v", sess)
	}
}

// Without a covering request the grant in force explains the session; an
// admin with no grant is explained by role; anything else is flagged.
func TestBuildSOC2RowsFallsBackToGrantRoleAndUnresolved(t *testing.T) {
	grant := &common.PermissionVersion{Permission: common.Permission{
		ID: "p1", UserID: "u-bob", MachineID: "m-web", RemoteUsers: []string{"deploy"},
		GrantedBy: "u-admin", GrantedAt: compNow.Add(-48 * time.Hour),
	}}
	rows := buildSOC2Rows(soc2Inputs{
		From: compNow.Add(-24 * time.Hour), To: compNow, Names: compNames(),
		Permissions: []*common.PermissionVersion{grant},
		Sessions: []*common.Session{
			{ID: "s-grant", UserID: "u-bob", MachineID: "m-web", RemoteUser: "deploy", StartTime: compNow.Add(-3 * time.Hour)},
			{ID: "s-role", UserID: "u-admin", MachineID: "m-web", RemoteUser: "root", StartTime: compNow.Add(-2 * time.Hour)},
			{ID: "s-none", UserID: "u-bob", MachineID: "m-web", RemoteUser: "root", StartTime: compNow.Add(-time.Hour)},
			{ID: "s-old", UserID: "u-bob", MachineID: "m-web", RemoteUser: "deploy", StartTime: compNow.Add(-72 * time.Hour)},
		},
	})
	got := map[string]accessEventRow{}
	for _, r := range rows {
		got[r.EventID] = r
	}
	if _, ok := got["s-old"]; ok {
		t.Error("a session before the range must not be reported")
	}
	if r := got["s-grant"]; r.Authorization != authzPermission || r.AuthorizedBy != "permission:p1" ||
		r.Approver != "admin" || r.Reason != "standing grant" {
		t.Errorf("grant session = %+v", r)
	}
	if r := got["s-role"]; r.Authorization != authzRole || r.AuthorizedBy != "role:admin" {
		t.Errorf("admin session = %+v", r)
	}
	if r := got["s-none"]; r.Authorization != authzUnresolved {
		t.Errorf("deploy grant must not explain a root session: %+v", r)
	}
}

// An access review as of a past instant includes grants since revoked and
// excludes ones not yet granted or already expired, and labels each as
// standing or time-boxed.
func TestBuildGrantReviewRowsAsOf(t *testing.T) {
	asOf := compNow.Add(-24 * time.Hour)
	versions := []*common.PermissionVersion{
		{Permission: common.Permission{ID: "standing", UserID: "u-alice", MachineID: "m-web", GrantedAt: compNow.Add(-30 * 24 * time.Hour), RemoteUsers: []string{"root"}}},
		{Permission: common.Permission{ID: "timeboxed", UserID: "u-bob", MachineID: "m-web", GrantedAt: asOf.Add(-time.Hour), ExpiresAt: ptrTime(asOf.Add(time.Hour))}},
		{Permission: common.Permission{ID: "revoked-later", UserID: "u-bob", MachineID: "m-pay", GrantedAt: asOf.Add(-time.Hour)}, SupersededAt: ptrTime(asOf.Add(time.Hour)), EndReason: "revoked"},
		{Permission: common.Permission{ID: "revoked-before", UserID: "u-bob", MachineID: "m-pay", GrantedAt: asOf.Add(-2 * time.Hour)}, SupersededAt: ptrTime(asOf.Add(-time.Hour)), EndReason: "revoked"},
		{Permission: common.Permission{ID: "future", UserID: "u-alice", MachineID: "m-pay", GrantedAt: asOf.Add(time.Hour)}},
		{Permission: common.Permission{ID: "expired", UserID: "u-alice", MachineID: "m-pay", GrantedAt: asOf.Add(-3 * time.Hour), ExpiresAt: ptrTime(asOf.Add(-time.Hour))}},
	}
	rows := buildGrantReviewRows(versions, asOf, compNames())
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.PermissionID+":"+r.Kind)
	}
	want := "standing:standing revoked-later:standing timeboxed:time-boxed"
	if got := strings.Join(ids, " "); got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
	if rows[1].EndReason != "revoked" || rows[1].EndedAt == "" {
		t.Errorf("a since-revoked grant must say when it ended: %+v", rows[1])
	}
}

func TestBuildPCIRowsOnlyTaggedTargets(t *testing.T) {
	rows := buildPCIRows([]*common.Session{
		{ID: "s-pay", UserID: "u-bob", MachineID: "m-pay", RemoteUser: "root", StartTime: compNow.Add(-time.Hour), RecordingPath: "/r/s-pay.cast"},
		{ID: "s-pay-app", UserID: "u-bob", MachineID: "m-pay", RemoteUser: "app", StartTime: compNow.Add(-2 * time.Hour)},
		{ID: "s-web", UserID: "u-bob", MachineID: "m-web", RemoteUser: "root", StartTime: compNow.Add(-time.Hour)},
	}, nil, compNow.Add(-24*time.Hour), compNow, compNames())
	if len(rows) != 2 || rows[0].SessionID != "s-pay-app" || rows[1].SessionID != "s-pay" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Privileged || !rows[1].Privileged || !rows[1].Recording {
		t.Errorf("privileged/recording flags wrong: %+v", rows)
	}
}

func TestParseReportRange(t *testing.T) {
	from, to, err := parseReportRange("2026-10-01", "2026-10-31", compNow)
	if err != nil {
		t.Fatal(err)
	}
	if !from.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || to.Day() != 31 || to.Hour() != 23 {
		t.Errorf("range = %v .. %v", from, to)
	}
	from, to, err = parseReportRange("", "", compNow)
	if err != nil || !to.Equal(compNow) || !from.Equal(compNow.Add(-defaultComplianceWindow)) {
		t.Errorf("default range = %v .. %v (%v)", from, to, err)
	}
	for _, bad := range [][2]string{{"yesterday", ""}, {"2026-10-31", "2026-10-01"}} {
		if _, _, err := parseReportRange(bad[0], bad[1], compNow); err == nil {
			t.Errorf("%v: expected an error", bad)
		}
	}
}

// --- through the API and Postgres --------------------------------------------

func TestComplianceReportsRequirePrivilegedViewer(t *testing.T) {
	e := newEnv(t, nil)
	for _, name := range []string{reportTypeSOC2Access, reportTypeISOReview, reportTypePCIAdmin} {
		e.expect(e.do(http.MethodGet, "/api/v1/reports/"+name+"/export", e.aliceKey, nil), http.StatusForbidden)
		e.expect(e.do(http.MethodGet, "/api/v1/reports/"+name+"/export?format=csv", e.auditorKey, nil), http.StatusOK)
	}
	e.expect(e.do(http.MethodGet, "/api/v1/reports/soc2-access/export?from=nope", e.auditorKey, nil), http.StatusBadRequest)
}

// The SOC2 report resolves the approval behind a real approved request and a
// session opened under it.
func TestSOC2ReportEndToEnd(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()

	r := e.do(http.MethodPost, "/api/v1/access-requests", e.aliceKey, map[string]any{
		"machine_id": e.machine.ID, "remote_users": []string{"root"}, "reason": "rotate certs", "duration": 3600,
	})
	e.expect(r, http.StatusCreated)
	reqID, _ := r.obj(t)["id"].(string)
	e.expect(e.do(http.MethodPost, "/api/v1/admin/access-requests/"+reqID+"/approve", e.adminKey, nil), http.StatusOK)

	sess := common.NewSession(e.alice.ID, e.machine.ID, "root", "")
	sess.Source = "ssh"
	if err := e.store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	r = e.do(http.MethodGet, "/api/v1/reports/soc2-access/export?format=json", e.auditorKey, nil)
	e.expect(r, http.StatusOK)
	var out struct {
		Records []accessEventRow `json:"records"`
	}
	r.json(t, &out)
	var found bool
	for _, row := range out.Records {
		if row.EventType == "session" && row.EventID == sess.ID {
			found = true
			if row.Reason != "rotate certs" || row.Approver != "admin" || row.AuthorizedBy != "access_request:"+reqID {
				t.Errorf("session row = %+v", row)
			}
		}
	}
	if !found {
		t.Fatalf("session %s missing from %s", sess.ID, r.body)
	}
}

// A grant revoked after the review date still appears in a review as of that
// date, because the trigger archived it.
func TestISOReviewIncludesSinceRevokedGrants(t *testing.T) {
	e := newEnv(t, nil)
	r := e.do(http.MethodPost, "/api/v1/admin/permissions", e.adminKey, map[string]any{
		"user_id": e.bob.ID, "machine_id": e.machine.ID, "access_type": "ssh", "remote_users": []string{"root"},
	})
	e.expect(r, http.StatusCreated)
	permID, _ := r.obj(t)["id"].(string)

	asOf := time.Now().UTC().Add(time.Second).Format(time.RFC3339)
	time.Sleep(1100 * time.Millisecond)
	e.expect(e.do(http.MethodDelete, "/api/v1/admin/permissions/"+permID, e.adminKey, nil), http.StatusOK)

	r = e.do(http.MethodGet, "/api/v1/reports/iso27001-access-review/export?format=csv&as_of="+asOf, e.auditorKey, nil)
	e.expect(r, http.StatusOK)
	recs, err := csv.NewReader(bytes.NewReader(r.body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var row []string
	for _, rec := range recs[1:] {
		if rec[0] == permID {
			row = rec
		}
	}
	if row == nil {
		t.Fatalf("revoked grant missing from the as-of review: %s", r.body)
	}
	if row[5] != "standing" || row[10] != "revoked" {
		t.Errorf("row = %v", row)
	}

	r = e.do(http.MethodGet, "/api/v1/reports/iso27001-access-review/export?format=csv", e.auditorKey, nil)
	if strings.Contains(string(r.body), permID) {
		t.Errorf("a review as of now must not list the revoked grant: %s", r.body)
	}
}

func TestPCIReportOnlyTaggedMachines(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	pay := common.NewMachine("pay-01", "10.0.0.9", 22, map[string]string{common.PCITagKey: common.PCITagValue})
	if err := e.store.CreateMachine(ctx, pay); err != nil {
		t.Fatal(err)
	}
	inScope := common.NewSession(e.bob.ID, pay.ID, "root", "")
	outOfScope := common.NewSession(e.bob.ID, e.machine.ID, "root", "")
	for _, s := range []*common.Session{inScope, outOfScope} {
		if err := e.store.CreateSession(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	r := e.do(http.MethodGet, "/api/v1/reports/pci-admin-activity/export?format=csv", e.auditorKey, nil)
	e.expect(r, http.StatusOK)
	body := string(r.body)
	if !strings.Contains(body, inScope.ID) || strings.Contains(body, outOfScope.ID) {
		t.Errorf("pci report = %s", body)
	}
}

// A request the risk policy approved names the policy, not a blank, as its
// approver, and so does the session it authorised.
func TestBuildSOC2RowsPolicyApproval(t *testing.T) {
	reviewed := compNow.Add(-2 * time.Hour)
	rows := buildSOC2Rows(soc2Inputs{
		From: compNow.Add(-24 * time.Hour), To: compNow, Names: compNames(),
		Requests: []*common.AccessRequest{{
			ID: "r1", UserID: "u-bob", MachineID: "m-web", RemoteUsers: []string{"deploy"},
			Status: "approved", RequestedAt: reviewed, ReviewedAt: &reviewed,
		}},
		Sessions: []*common.Session{{ID: "s1", UserID: "u-bob", MachineID: "m-web", RemoteUser: "deploy", StartTime: compNow.Add(-time.Hour)}},
	})
	for _, r := range rows {
		if r.Approver != riskPolicyApprover || r.ApproverID != "" {
			t.Errorf("%s row approver = %q/%q", r.EventType, r.Approver, r.ApproverID)
		}
	}
}

// Every compliance report renders as a PDF, including the truncation note.
func TestCompliancePDFs(t *testing.T) {
	e := newEnv(t, nil)
	for _, name := range []string{reportTypeSOC2Access, reportTypeISOReview, reportTypePCIAdmin} {
		r := e.do(http.MethodGet, "/api/v1/reports/"+name+"/export?format=pdf", e.auditorKey, nil)
		e.expect(r, http.StatusOK)
		if !bytes.HasPrefix(r.body, []byte("%PDF")) {
			t.Errorf("%s: not a PDF: %.20q", name, r.body)
		}
	}
	report := &complianceReport{Type: "t", Title: "T", Columns: []string{"a", "b"}, Truncated: true,
		Filters: map[string]string{"from": "x", "to": "y"}}
	for i := 0; i < 80; i++ { // enough rows to need a second page
		report.Rows = append(report.Rows, []string{"row", strings.Repeat("long value ", 20)})
	}
	if pdf, err := renderCompliancePDF(report, compNow); err != nil || len(pdf) == 0 {
		t.Errorf("renderCompliancePDF = %d bytes, %v", len(pdf), err)
	}
	e.expect(e.do(http.MethodGet, "/api/v1/reports/iso27001-access-review/export?as_of=soon", e.auditorKey, nil), http.StatusBadRequest)
}

// An update that kept granted_at leaves two versions covering the review
// time; only the one in force (the later) is listed.
func TestBuildGrantReviewRowsKeepsOneVersionPerGrant(t *testing.T) {
	granted := compNow.Add(-48 * time.Hour)
	ended := compNow.Add(time.Hour)
	versions := []*common.PermissionVersion{
		{Permission: common.Permission{ID: "p1", UserID: "u-alice", MachineID: "m-web", GrantedAt: granted, RemoteUsers: []string{"root"}},
			SupersededAt: &ended, EndReason: common.PermissionEndUpdated},
		{Permission: common.Permission{ID: "p1", UserID: "u-alice", MachineID: "m-web", GrantedAt: granted, RemoteUsers: []string{"root", "app"}}},
		{Permission: common.Permission{ID: "p2", UserID: "u-bob", MachineID: "m-web", GrantedAt: granted},
			SupersededAt: ptrTime(compNow.Add(2 * time.Hour)), EndReason: common.PermissionEndRevoked},
		{Permission: common.Permission{ID: "p2", UserID: "u-bob", MachineID: "m-web", GrantedAt: granted},
			SupersededAt: ptrTime(compNow.Add(3 * time.Hour)), EndReason: common.PermissionEndUpdated},
	}
	rows := buildGrantReviewRows(versions, compNow, compNames())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, r := range rows {
		switch r.PermissionID {
		case "p1":
			if len(r.RemoteUsers) != 2 || r.EndedAt != "" {
				t.Errorf("p1 should be the current version: %+v", r)
			}
		case "p2":
			if r.EndReason != common.PermissionEndUpdated {
				t.Errorf("p2 should be the version that ended last: %+v", r)
			}
		}
	}
}

// RDP, VNC and kubectl connections appear beside SSH sessions, tied to the
// grant or request of their own access type: an ssh+scp grant never explains
// an RDP connection.
func TestBuildSOC2RowsIncludesNonSSHConnections(t *testing.T) {
	reviewed := compNow.Add(-3 * time.Hour)
	both := &common.PermissionVersion{Permission: common.Permission{
		ID: "p-both", UserID: "u-bob", MachineID: "m-web", AccessType: "both", RemoteUsers: []string{"root"},
		GrantedBy: "u-admin", GrantedAt: compNow.Add(-48 * time.Hour),
	}}
	rdp := &common.PermissionVersion{Permission: common.Permission{
		ID: "p-rdp", UserID: "u-bob", MachineID: "m-web", AccessType: "rdp", RemoteUsers: []string{},
		GrantedBy: "u-admin", GrantedAt: compNow.Add(-48 * time.Hour),
	}}
	rows := buildSOC2Rows(soc2Inputs{
		From: compNow.Add(-24 * time.Hour), To: compNow,
		Names:       compNames(),
		Permissions: []*common.PermissionVersion{both, rdp},
		Requests: []*common.AccessRequest{{
			ID: "r-k8s", UserID: "u-alice", MachineID: "m-pay", AccessType: "k8s", RemoteUsers: []string{"payments/edit"},
			Reason: "INC-7", Status: "approved", RequestedAt: compNow.Add(-4 * time.Hour),
			ReviewedAt: &reviewed, ReviewedBy: ptrStr("u-admin"), ExpiresAt: ptrTime(compNow.Add(time.Hour)),
		}},
		Connections: []connection{
			{ID: "a-rdp", UserID: "u-bob", MachineID: "m-web", AccessType: "rdp", At: compNow.Add(-2 * time.Hour)},
			{ID: "a-vnc", UserID: "u-bob", MachineID: "m-web", AccessType: "vnc", At: compNow.Add(-90 * time.Minute)},
			{ID: "a-k8s", UserID: "u-alice", MachineID: "m-pay", AccessType: "k8s", At: compNow.Add(-time.Hour)},
			{ID: "a-old", UserID: "u-bob", MachineID: "m-web", AccessType: "rdp", At: compNow.Add(-48 * time.Hour)},
		},
	})
	byID := map[string]accessEventRow{}
	for _, r := range rows {
		byID[r.EventID] = r
	}
	if _, ok := byID["a-old"]; ok {
		t.Error("connection outside the range was included")
	}
	if r := byID["a-rdp"]; r.EventType != "rdp_session" || r.AuthorizedBy != "permission:p-rdp" {
		t.Errorf("rdp row = %+v", r)
	}
	// Only the ssh+scp grant covers bob for vnc, and it must not.
	if r := byID["a-vnc"]; r.EventType != "vnc_session" || r.Authorization != authzUnresolved {
		t.Errorf("vnc row = %+v", r)
	}
	if r := byID["a-k8s"]; r.EventType != "k8s_session" || r.AuthorizedBy != "access_request:r-k8s" || r.Approver != "admin" {
		t.Errorf("k8s row = %+v", r)
	}
	if r := byID["r-k8s"]; r.RemoteUser != "payments/edit" {
		t.Errorf("k8s request should show its scope: %+v", r)
	}
}

func TestBuildPCIRowsIncludesConnections(t *testing.T) {
	rows := buildPCIRows(nil, []connection{
		{ID: "c-pay", UserID: "u-bob", MachineID: "m-pay", AccessType: "rdp", At: compNow.Add(-time.Hour)},
		{ID: "c-web", UserID: "u-bob", MachineID: "m-web", AccessType: "k8s", At: compNow.Add(-time.Hour)},
	}, compNow.Add(-24*time.Hour), compNow, compNames())
	if len(rows) != 1 || rows[0].SessionID != "c-pay" || rows[0].Source != "rdp" || rows[0].Recording {
		t.Fatalf("rows = %+v", rows)
	}
}

// Audited RDP and kubectl connections reach the exported report.
func TestComplianceExportIncludesAuditedConnections(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	rdp := common.NewAuditLog(e.alice.ID, "tunnel.open", "machine:"+e.machine.ID, "10.0.0.9", map[string]interface{}{"protocol": "rdp"})
	k8s := common.NewAuditLog(e.alice.ID, "k8s.access.open", "machine:"+e.machine.ID, "10.0.0.9", map[string]interface{}{})
	for _, l := range []*common.AuditLog{rdp, k8s} {
		if err := e.store.CreateAuditLog(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	r := e.do(http.MethodGet, "/api/v1/reports/soc2-access/export?format=json", e.auditorKey, nil)
	e.expect(r, http.StatusOK)
	var out struct {
		Records []accessEventRow `json:"records"`
	}
	r.json(t, &out)
	types := map[string]string{}
	for _, row := range out.Records {
		types[row.EventID] = row.EventType
	}
	if types[rdp.ID] != "rdp_session" || types[k8s.ID] != "k8s_session" {
		t.Errorf("connection rows = %v", types)
	}
}
