package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-pdf/fpdf"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// Compliance report names. They are organisation-wide, so only privileged
// viewers (admin, operator, auditor) may export them.
const (
	reportTypeSOC2Access = "soc2-access"
	reportTypeISOReview  = "iso27001-access-review"
	reportTypePCIAdmin   = "pci-admin-activity"
)

// PCITagKey and PCITagValue mark a machine as PCI-in-scope:
// tags: {pci: in-scope}.
const (
	PCITagKey   = "pci"
	PCITagValue = "in-scope"
)

// complianceFetchLimit bounds how many rows of each source a report reads.
// A report that hits it says so with truncated=true instead of silently
// dropping events.
const complianceFetchLimit = 5000

// defaultComplianceWindow is the range used when the caller gives no from.
const defaultComplianceWindow = 30 * 24 * time.Hour

// Ways a session's authorisation can be explained in the SOC2 report.
const (
	authzAccessRequest = "access_request"
	authzPermission    = "permission"
	authzRole          = "role"
	authzUnresolved    = "unresolved"
)

func isComplianceReport(name string) bool {
	switch name {
	case reportTypeSOC2Access, reportTypeISOReview, reportTypePCIAdmin:
		return true
	}
	return false
}

// complianceReport is a rendered-agnostic report: Columns/Rows feed CSV and
// PDF, Records feeds JSON.
type complianceReport struct {
	Type      string
	Title     string
	Filters   map[string]string
	Columns   []string
	Rows      [][]string
	Records   any
	Truncated bool
}

type complianceEnvelope struct {
	ReportType string            `json:"report_type"`
	Format     string            `json:"format"`
	Generated  string            `json:"generated_at"`
	Filters    map[string]string `json:"filters,omitempty"`
	Count      int               `json:"record_count"`
	Truncated  bool              `json:"truncated,omitempty"`
	Records    any               `json:"records"`
}

// accessEventRow is one row of the SOC2 access report: who reached what,
// when, why, and who approved it.
type accessEventRow struct {
	EventType     string `json:"event_type"` // session | access_request
	EventID       string `json:"event_id"`
	Timestamp     string `json:"timestamp"`
	ActorID       string `json:"actor_id"`
	Actor         string `json:"actor"`
	TargetID      string `json:"target_id"`
	Target        string `json:"target"`
	RemoteUser    string `json:"remote_user,omitempty"`
	Source        string `json:"source,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	ApproverID    string `json:"approver_id,omitempty"`
	Approver      string `json:"approver,omitempty"`
	Authorization string `json:"authorization"`
	AuthorizedBy  string `json:"authorized_by,omitempty"` // access_request:<id> | permission:<id> | role:<role>
}

// grantReviewRow is one grant in the ISO27001 access review.
type grantReviewRow struct {
	PermissionID string   `json:"permission_id"`
	UserID       string   `json:"user_id"`
	User         string   `json:"user"`
	MachineID    string   `json:"machine_id"`
	Machine      string   `json:"machine"`
	AccessType   string   `json:"access_type"`
	RemoteUsers  []string `json:"remote_users"`
	Kind         string   `json:"kind"` // standing | time-boxed
	GrantedByID  string   `json:"granted_by_id,omitempty"`
	GrantedBy    string   `json:"granted_by,omitempty"`
	GrantedAt    string   `json:"granted_at"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	EndedAt      string   `json:"ended_at,omitempty"` // when this version was later changed or revoked
	EndReason    string   `json:"end_reason,omitempty"`
}

// pciActivityRow is one privileged session against a PCI-in-scope target.
type pciActivityRow struct {
	SessionID  string `json:"session_id"`
	ActorID    string `json:"actor_id"`
	Actor      string `json:"actor"`
	TargetID   string `json:"target_id"`
	Target     string `json:"target"`
	RemoteUser string `json:"remote_user"`
	Privileged bool   `json:"privileged"`
	Source     string `json:"source"`
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time,omitempty"`
	Status     string `json:"status"`
	Recording  bool   `json:"recording_available"`
}

// exportComplianceReport serves the compliance reports.
func (s *APIServer) exportComplianceReport(c *gin.Context, name, format string) {
	if !isPrivilegedViewer(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "compliance reports require the admin, operator, or auditor role"})
		return
	}
	now := time.Now().UTC()

	var (
		report *complianceReport
		err    error
	)
	switch name {
	case reportTypeSOC2Access, reportTypePCIAdmin:
		from, to, perr := parseReportRange(c.Query("from"), c.Query("to"), now)
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": perr.Error()})
			return
		}
		if name == reportTypeSOC2Access {
			report, err = s.buildSOC2Report(c, from, to)
		} else {
			report, err = s.buildPCIReport(c, from, to)
		}
	case reportTypeISOReview:
		asOf := now
		if raw := strings.TrimSpace(c.Query("as_of")); raw != "" {
			t, perr := parseReportTime(raw, true)
			if perr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "as_of: " + perr.Error()})
				return
			}
			asOf = t
		}
		report, err = s.buildISOReviewReport(c, asOf)
	}
	if err != nil {
		s.logger.Error("Failed to build %s report: %v", name, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build report"})
		return
	}
	writeComplianceReport(c, format, report, now)
}

// parseReportRange reads from/to (RFC3339 or YYYY-MM-DD). A date-only "to"
// covers that whole day. Defaults: the last 30 days up to now.
func parseReportRange(rawFrom, rawTo string, now time.Time) (time.Time, time.Time, error) {
	to := now
	if strings.TrimSpace(rawTo) != "" {
		t, err := parseReportTime(rawTo, true)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
		}
		to = t
	}
	from := to.Add(-defaultComplianceWindow)
	if strings.TrimSpace(rawFrom) != "" {
		t, err := parseReportTime(rawFrom, false)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
		}
		from = t
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	return from, to, nil
}

// parseReportTime accepts RFC3339 or a bare date. endOfDay moves a bare date
// to the last instant of that day so "to=2026-10-31" includes the 31st.
func parseReportTime(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errors.New("must be RFC3339 or YYYY-MM-DD")
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Nanosecond)
	}
	return t, nil
}

func inRange(t, from, to time.Time) bool {
	t = t.UTC()
	return !t.Before(from) && !t.After(to)
}

func rangeFilters(from, to time.Time) map[string]string {
	return map[string]string{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}
}

// complianceNames resolves user and machine ids to names for every report.
type complianceNames struct {
	users    map[string]*common.User
	machines map[string]*common.Machine
}

func (s *APIServer) loadComplianceNames(c *gin.Context) (*complianceNames, error) {
	ctx := c.Request.Context()
	users, err := s.store.ListUsers(ctx, complianceFetchLimit, 0)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	machines, err := s.store.ListMachines(ctx, complianceFetchLimit, 0)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	n := &complianceNames{users: map[string]*common.User{}, machines: map[string]*common.Machine{}}
	for _, u := range users {
		n.users[u.ID] = u
	}
	for _, m := range machines {
		n.machines[m.ID] = m
	}
	return n, nil
}

// user returns the username, or the id when the account no longer exists.
func (n *complianceNames) user(id string) string {
	if u, ok := n.users[id]; ok {
		return u.Username
	}
	return id
}

func (n *complianceNames) machine(id string) string {
	if m, ok := n.machines[id]; ok {
		return m.Name
	}
	return id
}

// dbInstant turns a timestamp read from a TIMESTAMP (without time zone)
// column back into the instant that was written. Writers store the server's
// local wall clock and lib/pq returns it labelled UTC; on a UTC host this is
// a no-op.
func dbInstant(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local).UTC()
}

func dbInstantPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := dbInstant(*t)
	return &v
}

func normalizeSessions(sessions []*common.Session) {
	for _, s := range sessions {
		s.StartTime = dbInstant(s.StartTime)
		s.EndTime = dbInstantPtr(s.EndTime)
	}
}

func normalizeRequests(requests []*common.AccessRequest) {
	for _, r := range requests {
		r.RequestedAt = dbInstant(r.RequestedAt)
		r.ReviewedAt = dbInstantPtr(r.ReviewedAt)
		r.ExpiresAt = dbInstantPtr(r.ExpiresAt)
	}
}

func normalizeVersions(versions []*common.PermissionVersion) {
	for _, v := range versions {
		v.GrantedAt = dbInstant(v.GrantedAt)
		v.ExpiresAt = dbInstantPtr(v.ExpiresAt)
		v.SupersededAt = dbInstantPtr(v.SupersededAt)
	}
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// --- SOC2 ---------------------------------------------------------------------

type soc2Inputs struct {
	From, To    time.Time
	Sessions    []*common.Session
	Requests    []*common.AccessRequest
	Permissions []*common.PermissionVersion
	Names       *complianceNames
}

func (s *APIServer) buildSOC2Report(c *gin.Context, from, to time.Time) (*complianceReport, error) {
	ctx := c.Request.Context()
	names, err := s.loadComplianceNames(c)
	if err != nil {
		return nil, err
	}
	sessions, err := s.store.ListSessions(ctx, complianceFetchLimit, 0)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	requests, err := s.store.ListAllAccessRequests(ctx, complianceFetchLimit, 0)
	if err != nil {
		return nil, fmt.Errorf("list access requests: %w", err)
	}
	perms, err := s.store.ListPermissionVersions(ctx, complianceFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("list permission versions: %w", err)
	}
	normalizeSessions(sessions)
	normalizeRequests(requests)
	normalizeVersions(perms)

	rows := buildSOC2Rows(soc2Inputs{From: from, To: to, Sessions: sessions, Requests: requests, Permissions: perms, Names: names})
	report := &complianceReport{
		Type:    reportTypeSOC2Access,
		Title:   "Orion Belt Access Report (SOC 2)",
		Filters: rangeFilters(from, to),
		Columns: []string{"event_type", "event_id", "timestamp", "actor", "target", "remote_user", "status", "reason", "approver", "authorization", "authorized_by"},
		Records: rows,
		// Either source may have older events past the cap; say so when the
		// oldest row read is still inside the range.
		Truncated: (len(sessions) == complianceFetchLimit && inRange(sessions[len(sessions)-1].StartTime, from, to)) ||
			(len(requests) == complianceFetchLimit && inRange(requests[len(requests)-1].RequestedAt, from, to)),
	}
	for _, r := range rows {
		report.Rows = append(report.Rows, []string{r.EventType, r.EventID, r.Timestamp, r.Actor, r.Target, r.RemoteUser, r.Status, r.Reason, r.Approver, r.Authorization, r.AuthorizedBy})
	}
	return report, nil
}

// buildSOC2Rows lists every session started and every access request made in
// the range, oldest first. Each session is tied to what authorised it: the
// approved request covering its start, otherwise the grant in force, otherwise
// the user's privileged role.
func buildSOC2Rows(in soc2Inputs) []accessEventRow {
	rows := []accessEventRow{}
	for _, sess := range in.Sessions {
		if !inRange(sess.StartTime, in.From, in.To) {
			continue
		}
		row := accessEventRow{
			EventType:  "session",
			EventID:    sess.ID,
			Timestamp:  sess.StartTime.UTC().Format(time.RFC3339),
			ActorID:    sess.UserID,
			Actor:      in.Names.user(sess.UserID),
			TargetID:   sess.MachineID,
			Target:     in.Names.machine(sess.MachineID),
			RemoteUser: sess.RemoteUser,
			Source:     sess.Source,
			Status:     sess.Status,
		}
		explainSession(&row, sess, in)
		rows = append(rows, row)
	}
	for _, req := range in.Requests {
		if !inRange(req.RequestedAt, in.From, in.To) {
			continue
		}
		row := accessEventRow{
			EventType:     "access_request",
			EventID:       req.ID,
			Timestamp:     req.RequestedAt.UTC().Format(time.RFC3339),
			ActorID:       req.UserID,
			Actor:         in.Names.user(req.UserID),
			TargetID:      req.MachineID,
			Target:        in.Names.machine(req.MachineID),
			RemoteUser:    strings.Join(req.RemoteUsers, " "),
			Status:        req.Status,
			Reason:        req.Reason,
			Authorization: authzAccessRequest,
			AuthorizedBy:  "access_request:" + req.ID,
		}
		if req.ReviewedBy != nil {
			row.ApproverID = *req.ReviewedBy
			row.Approver = in.Names.user(*req.ReviewedBy)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Timestamp < rows[j].Timestamp })
	return rows
}

func explainSession(row *accessEventRow, sess *common.Session, in soc2Inputs) {
	start := sess.StartTime.UTC()

	var best *common.AccessRequest
	for _, req := range in.Requests {
		if req.Status != "approved" || req.ReviewedAt == nil ||
			req.UserID != sess.UserID || req.MachineID != sess.MachineID {
			continue
		}
		if req.ReviewedAt.UTC().After(start) || (req.ExpiresAt != nil && !req.ExpiresAt.UTC().After(start)) {
			continue
		}
		if !slices.Contains(req.RemoteUsers, sess.RemoteUser) {
			continue
		}
		if best == nil || req.ReviewedAt.After(*best.ReviewedAt) {
			best = req
		}
	}
	if best != nil {
		row.Authorization = authzAccessRequest
		row.AuthorizedBy = "access_request:" + best.ID
		row.Reason = best.Reason
		row.ApproverID = *best.ReviewedBy
		row.Approver = in.Names.user(*best.ReviewedBy)
		return
	}

	for _, p := range in.Permissions {
		if p.UserID != sess.UserID || p.MachineID != sess.MachineID || !p.ActiveAt(start) {
			continue
		}
		if !slices.Contains(p.RemoteUsers, sess.RemoteUser) {
			continue
		}
		row.Authorization = authzPermission
		row.AuthorizedBy = "permission:" + p.ID
		row.Reason = grantKind(p.ExpiresAt) + " grant"
		row.ApproverID = p.GrantedBy
		row.Approver = in.Names.user(p.GrantedBy)
		return
	}

	if u, ok := in.Names.users[sess.UserID]; ok && (u.IsAdmin || u.EffectiveRole() == common.RoleAdmin || u.EffectiveRole() == common.RoleOperator) {
		row.Authorization = authzRole
		row.AuthorizedBy = "role:" + u.EffectiveRole()
		row.Reason = "privileged role"
		return
	}
	row.Authorization = authzUnresolved
}

func endsLater(a, b *common.PermissionVersion) bool {
	if a.SupersededAt == nil {
		return true
	}
	return b.SupersededAt != nil && a.SupersededAt.After(*b.SupersededAt)
}

func grantKind(expiresAt *time.Time) string {
	if expiresAt == nil {
		return "standing"
	}
	return "time-boxed"
}

// --- ISO 27001 ----------------------------------------------------------------

func (s *APIServer) buildISOReviewReport(c *gin.Context, asOf time.Time) (*complianceReport, error) {
	names, err := s.loadComplianceNames(c)
	if err != nil {
		return nil, err
	}
	versions, err := s.store.ListPermissionVersions(c.Request.Context(), complianceFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("list permission versions: %w", err)
	}
	normalizeVersions(versions)
	rows := buildGrantReviewRows(versions, asOf, names)
	report := &complianceReport{
		Type:      reportTypeISOReview,
		Title:     "Orion Belt Privileged Access Review (ISO 27001)",
		Filters:   map[string]string{"as_of": asOf.Format(time.RFC3339)},
		Columns:   []string{"permission_id", "user", "machine", "access_type", "remote_users", "kind", "granted_by", "granted_at", "expires_at", "ended_at", "end_reason"},
		Records:   rows,
		Truncated: len(versions) == complianceFetchLimit,
	}
	for _, r := range rows {
		report.Rows = append(report.Rows, []string{r.PermissionID, r.User, r.Machine, r.AccessType, strings.Join(r.RemoteUsers, " "), r.Kind, r.GrantedBy, r.GrantedAt, r.ExpiresAt, r.EndedAt, r.EndReason})
	}
	return report, nil
}

// buildGrantReviewRows lists the grant versions in force at asOf, standing
// grants first, then time-boxed ones by expiry.
func buildGrantReviewRows(versions []*common.PermissionVersion, asOf time.Time, names *complianceNames) []grantReviewRow {
	// An update that kept granted_at leaves two versions covering asOf; the
	// one that ended later (or has not ended) is what applied.
	active := map[string]*common.PermissionVersion{}
	var order []string
	for _, v := range versions {
		if !v.ActiveAt(asOf) {
			continue
		}
		prev, seen := active[v.ID]
		if !seen {
			order = append(order, v.ID)
		}
		if !seen || endsLater(v, prev) {
			active[v.ID] = v
		}
	}

	rows := []grantReviewRow{}
	for _, id := range order {
		v := active[id]
		rows = append(rows, grantReviewRow{
			PermissionID: v.ID,
			UserID:       v.UserID,
			User:         names.user(v.UserID),
			MachineID:    v.MachineID,
			Machine:      names.machine(v.MachineID),
			AccessType:   v.AccessType,
			RemoteUsers:  v.RemoteUsers,
			Kind:         grantKind(v.ExpiresAt),
			GrantedByID:  v.GrantedBy,
			GrantedBy:    names.user(v.GrantedBy),
			GrantedAt:    v.GrantedAt.UTC().Format(time.RFC3339),
			ExpiresAt:    formatOptionalTime(v.ExpiresAt),
			EndedAt:      formatOptionalTime(v.SupersededAt),
			EndReason:    v.EndReason,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Kind != b.Kind {
			return a.Kind == "standing"
		}
		if a.ExpiresAt != b.ExpiresAt {
			return a.ExpiresAt < b.ExpiresAt
		}
		if a.User != b.User {
			return a.User < b.User
		}
		return a.Machine < b.Machine
	})
	return rows
}

// --- PCI ----------------------------------------------------------------------

// IsPCIInScope reports whether a machine carries the pci: in-scope tag.
func IsPCIInScope(m *common.Machine) bool {
	if m == nil {
		return false
	}
	v, ok := m.Tags[PCITagKey]
	return ok && strings.EqualFold(strings.TrimSpace(v), PCITagValue)
}

// privilegedRemoteUsers are the target accounts a PCI report flags as
// administrative even without a session-level command log.
var privilegedRemoteUsers = map[string]bool{"root": true, "administrator": true, "admin": true}

func (s *APIServer) buildPCIReport(c *gin.Context, from, to time.Time) (*complianceReport, error) {
	names, err := s.loadComplianceNames(c)
	if err != nil {
		return nil, err
	}
	sessions, err := s.store.ListSessions(c.Request.Context(), complianceFetchLimit, 0)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	normalizeSessions(sessions)
	rows := buildPCIRows(sessions, from, to, names)
	report := &complianceReport{
		Type:      reportTypePCIAdmin,
		Title:     "Orion Belt Admin Activity on PCI Targets",
		Filters:   rangeFilters(from, to),
		Columns:   []string{"session_id", "actor", "target", "remote_user", "privileged", "source", "start_time", "end_time", "status", "recording_available"},
		Records:   rows,
		Truncated: len(sessions) == complianceFetchLimit && inRange(sessions[len(sessions)-1].StartTime, from, to),
	}
	report.Filters["tag"] = PCITagKey + "=" + PCITagValue
	for _, r := range rows {
		report.Rows = append(report.Rows, []string{r.SessionID, r.Actor, r.Target, r.RemoteUser, fmt.Sprint(r.Privileged), r.Source, r.StartTime, r.EndTime, r.Status, fmt.Sprint(r.Recording)})
	}
	return report, nil
}

// buildPCIRows keeps sessions in range whose target is tagged PCI-in-scope.
// Every session through the gateway is privileged access to the target; the
// privileged column additionally flags administrative target accounts.
func buildPCIRows(sessions []*common.Session, from, to time.Time, names *complianceNames) []pciActivityRow {
	rows := []pciActivityRow{}
	for _, sess := range sessions {
		if !inRange(sess.StartTime, from, to) || !IsPCIInScope(names.machines[sess.MachineID]) {
			continue
		}
		rows = append(rows, pciActivityRow{
			SessionID:  sess.ID,
			ActorID:    sess.UserID,
			Actor:      names.user(sess.UserID),
			TargetID:   sess.MachineID,
			Target:     names.machine(sess.MachineID),
			RemoteUser: sess.RemoteUser,
			Privileged: privilegedRemoteUsers[strings.ToLower(sess.RemoteUser)],
			Source:     sess.Source,
			StartTime:  sess.StartTime.UTC().Format(time.RFC3339),
			EndTime:    formatOptionalTime(sess.EndTime),
			Status:     sess.Status,
			Recording:  sess.RecordingPath != "",
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].StartTime < rows[j].StartTime })
	return rows
}

// --- rendering ----------------------------------------------------------------

func writeComplianceReport(c *gin.Context, format string, r *complianceReport, generatedAt time.Time) {
	filename := reportFilename(r.Type, format, generatedAt)
	var (
		payload     []byte
		err         error
		contentType string
	)
	switch format {
	case "csv":
		payload, err = renderComplianceCSV(r)
		contentType = "text/csv; charset=utf-8"
	case "pdf":
		payload, err = renderCompliancePDF(r, generatedAt)
		contentType = "application/pdf"
	default:
		payload, err = renderComplianceJSON(r, format, generatedAt)
		contentType = "application/json; charset=utf-8"
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render " + format})
		return
	}
	writeDownload(c, contentType, filename, payload)
}

func renderComplianceCSV(r *complianceReport) ([]byte, error) {
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	if err := w.Write(r.Columns); err != nil {
		return nil, err
	}
	if err := w.WriteAll(r.Rows); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderComplianceJSON(r *complianceReport, format string, generatedAt time.Time) ([]byte, error) {
	return json.MarshalIndent(complianceEnvelope{
		ReportType: r.Type,
		Format:     format,
		Generated:  generatedAt.UTC().Format(time.RFC3339),
		Filters:    r.Filters,
		Count:      len(r.Rows),
		Truncated:  r.Truncated,
		Records:    r.Records,
	}, "", "  ")
}

// pdfPrintableWidth is the usable width of a landscape A4 page in mm.
const pdfPrintableWidth = 277.0

func renderCompliancePDF(r *complianceReport, generatedAt time.Time) ([]byte, error) {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "B", 14)
	pdf.Cell(0, 8, r.Title)
	pdf.Ln(9)

	pdf.SetFont("Arial", "", 9)
	pdf.Cell(0, 6, fmt.Sprintf("Generated at: %s UTC", generatedAt.Format("2006-01-02 15:04:05")))
	pdf.Ln(5)
	keys := make([]string, 0, len(r.Filters))
	for k := range r.Filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pdf.Cell(0, 6, fmt.Sprintf("%s: %s", k, r.Filters[k]))
		pdf.Ln(5)
	}
	records := fmt.Sprintf("Records: %d", len(r.Rows))
	if r.Truncated {
		records += " (truncated: narrow the range for a complete report)"
	}
	pdf.Cell(0, 6, records)
	pdf.Ln(8)

	width := pdfPrintableWidth / float64(len(r.Columns))
	widths := make([]float64, len(r.Columns))
	for i := range widths {
		widths[i] = width
	}
	// Roughly two characters per millimetre at 7pt.
	maxChars := int(width * 2)
	writePDFHeader(pdf, r.Columns, widths)
	pdf.SetFont("Arial", "", 7)
	for _, row := range r.Rows {
		if pdf.GetY() > 190 {
			pdf.AddPage()
			writePDFHeader(pdf, r.Columns, widths)
			pdf.SetFont("Arial", "", 7)
		}
		for i, v := range row {
			pdf.CellFormat(widths[i], 6, truncateForPDF(v, maxChars), "1", 0, "L", false, 0, "")
		}
		pdf.Ln(-1)
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
