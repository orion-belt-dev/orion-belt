package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-pdf/fpdf"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

const (
	reportTypeSessions = "sessions"
	reportTypeAudit    = "audit"
)

type sessionReportRow struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	MachineID  string `json:"machine_id"`
	Machine    string `json:"machine"`
	RemoteUser string `json:"remote_user"`
	Source     string `json:"source"`
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time,omitempty"`
	Status     string `json:"status"`
}

type auditReportRow struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id"`
	Username  string                 `json:"username"`
	Action    string                 `json:"action"`
	Resource  string                 `json:"resource,omitempty"`
	IPAddress string                 `json:"ip_address,omitempty"`
	Timestamp string                 `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

type reportEnvelope struct {
	ReportType string            `json:"report_type"`
	Format     string            `json:"format"`
	Generated  string            `json:"generated_at"`
	Filters    map[string]string `json:"filters,omitempty"`
	Count      int               `json:"record_count"`
	Records    interface{}       `json:"records"`
}

func (s *APIServer) exportReport(c *gin.Context) {
	reportName := strings.ToLower(strings.TrimSpace(c.Param("name")))
	if reportName == "audit-logs" {
		reportName = reportTypeAudit
	}
	if reportName != reportTypeSessions && reportName != reportTypeAudit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "report must be one of: sessions, audit"})
		return
	}

	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "json")))
	if format != "csv" && format != "pdf" && format != "json" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be one of: csv, pdf, json"})
		return
	}

	generatedAt := time.Now().UTC()
	filters := collectReportFilters(c)

	switch reportName {
	case reportTypeSessions:
		records, err := s.buildSessionReportRows(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		s.writeSessionReport(c, format, records, filters, generatedAt)
	case reportTypeAudit:
		records, err := s.buildAuditReportRows(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		s.writeAuditReport(c, format, records, filters, generatedAt)
	}
}

func collectReportFilters(c *gin.Context) map[string]string {
	filters := map[string]string{}
	for _, key := range []string{"status", "action", "actor", "q", "limit"} {
		if v := strings.TrimSpace(c.Query(key)); v != "" {
			filters[key] = v
		}
	}
	if len(filters) == 0 {
		return nil
	}
	return filters
}

func parseLimit(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (s *APIServer) buildSessionReportRows(c *gin.Context) ([]sessionReportRow, error) {
	ctx := c.Request.Context()
	privileged := isPrivilegedViewer(c)
	uid := c.GetString("user_id")
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	search := strings.ToLower(strings.TrimSpace(c.Query("q")))
	limit := parseLimit(c.Query("limit"), 1000, 5000)

	var (
		sessions []*common.Session
		err      error
	)

	if status == "active" {
		sessions, err = s.store.ListActiveSessions(ctx)
	} else if privileged {
		sessions, err = s.store.ListSessions(ctx, limit, 0)
	} else {
		sessions, err = s.store.ListUserSessions(ctx, uid, limit, 0)
	}
	if err != nil {
		return nil, err
	}

	if !privileged {
		sessions = filterSessionsByUser(sessions, uid)
	}
	if status != "" {
		filtered := sessions[:0]
		for _, sess := range sessions {
			if strings.EqualFold(sess.Status, status) {
				filtered = append(filtered, sess)
			}
		}
		sessions = filtered
	}

	users, _ := s.store.ListUsers(ctx, 2000, 0)
	userNames := make(map[string]string, len(users))
	for _, u := range users {
		userNames[u.ID] = u.Username
	}
	machines, _ := s.store.ListMachines(ctx, 2000, 0)
	machineNames := make(map[string]string, len(machines))
	for _, m := range machines {
		machineNames[m.ID] = m.Name
	}

	rows := make([]sessionReportRow, 0, len(sessions))
	for _, sess := range sessions {
		username := userNames[sess.UserID]
		if username == "" {
			username = sess.UserID
		}
		machine := machineNames[sess.MachineID]
		if machine == "" {
			machine = sess.MachineID
		}
		row := sessionReportRow{
			ID:         sess.ID,
			UserID:     sess.UserID,
			Username:   username,
			MachineID:  sess.MachineID,
			Machine:    machine,
			RemoteUser: sess.RemoteUser,
			Source:     sess.Source,
			StartTime:  sess.StartTime.UTC().Format(time.RFC3339),
			Status:     sess.Status,
		}
		if sess.EndTime != nil {
			row.EndTime = sess.EndTime.UTC().Format(time.RFC3339)
		}
		if search != "" {
			blob := strings.ToLower(strings.Join([]string{
				row.ID,
				row.UserID,
				row.Username,
				row.MachineID,
				row.Machine,
				row.RemoteUser,
				row.Source,
				row.Status,
			}, " "))
			if !strings.Contains(blob, search) {
				continue
			}
		}
		rows = append(rows, row)
	}

	return rows, nil
}

func (s *APIServer) buildAuditReportRows(c *gin.Context) ([]auditReportRow, error) {
	ctx := c.Request.Context()
	privileged := isPrivilegedViewer(c)
	limit := parseLimit(c.Query("limit"), 500, 5000)
	action := strings.TrimSpace(c.Query("action"))
	actor := strings.ToLower(strings.TrimSpace(c.Query("actor")))
	search := strings.ToLower(strings.TrimSpace(c.Query("q")))

	filters := map[string]interface{}{}
	if action != "" {
		filters["action"] = action
	}
	if !privileged {
		filters["user_id"] = c.GetString("user_id")
	}

	logs, err := s.store.ListAuditLogs(ctx, limit, 0, filters)
	if err != nil {
		return nil, err
	}
	users, _ := s.store.ListUsers(ctx, 2000, 0)
	userNames := make(map[string]string, len(users))
	for _, u := range users {
		userNames[u.ID] = u.Username
	}

	rows := make([]auditReportRow, 0, len(logs))
	for _, l := range logs {
		username := userNames[l.UserID]
		if username == "" {
			username = l.UserID
		}
		if actor != "" {
			if !strings.Contains(strings.ToLower(username), actor) && !strings.Contains(strings.ToLower(l.UserID), actor) {
				continue
			}
		}
		row := auditReportRow{
			ID:        l.ID,
			UserID:    l.UserID,
			Username:  username,
			Action:    l.Action,
			Resource:  l.Resource,
			IPAddress: l.IPAddress,
			Timestamp: l.Timestamp.UTC().Format(time.RFC3339),
			Metadata:  l.Metadata,
		}
		if search != "" {
			metaJSON, _ := json.Marshal(row.Metadata)
			blob := strings.ToLower(strings.Join([]string{
				row.ID,
				row.UserID,
				row.Username,
				row.Action,
				row.Resource,
				row.IPAddress,
				string(metaJSON),
			}, " "))
			if !strings.Contains(blob, search) {
				continue
			}
		}
		rows = append(rows, row)
	}

	return rows, nil
}

func (s *APIServer) writeSessionReport(c *gin.Context, format string, rows []sessionReportRow, filters map[string]string, generatedAt time.Time) {
	filename := reportFilename(reportTypeSessions, format, generatedAt)
	switch format {
	case "csv":
		payload, err := renderSessionCSV(rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render csv"})
			return
		}
		writeDownload(c, "text/csv; charset=utf-8", filename, payload)
	case "pdf":
		payload, err := renderSessionPDF(rows, filters, generatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render pdf"})
			return
		}
		writeDownload(c, "application/pdf", filename, payload)
	case "json":
		payload, err := renderReportJSON(reportTypeSessions, format, filters, generatedAt, rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render json"})
			return
		}
		writeDownload(c, "application/json; charset=utf-8", filename, payload)
	}
}

func (s *APIServer) writeAuditReport(c *gin.Context, format string, rows []auditReportRow, filters map[string]string, generatedAt time.Time) {
	filename := reportFilename(reportTypeAudit, format, generatedAt)
	switch format {
	case "csv":
		payload, err := renderAuditCSV(rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render csv"})
			return
		}
		writeDownload(c, "text/csv; charset=utf-8", filename, payload)
	case "pdf":
		payload, err := renderAuditPDF(rows, filters, generatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render pdf"})
			return
		}
		writeDownload(c, "application/pdf", filename, payload)
	case "json":
		payload, err := renderReportJSON(reportTypeAudit, format, filters, generatedAt, rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render json"})
			return
		}
		writeDownload(c, "application/json; charset=utf-8", filename, payload)
	}
}

func writeDownload(c *gin.Context, contentType, filename string, payload []byte) {
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(http.StatusOK, contentType, payload)
}

func reportFilename(reportName, format string, t time.Time) string {
	return fmt.Sprintf("%s-%s.%s", reportName, t.UTC().Format("20060102-150405"), format)
}

func renderReportJSON(reportName, format string, filters map[string]string, generatedAt time.Time, records interface{}) ([]byte, error) {
	count := 0
	switch rows := records.(type) {
	case []sessionReportRow:
		count = len(rows)
	case []auditReportRow:
		count = len(rows)
	}
	payload := reportEnvelope{
		ReportType: reportName,
		Format:     format,
		Generated:  generatedAt.UTC().Format(time.RFC3339),
		Filters:    filters,
		Count:      count,
		Records:    records,
	}
	return json.MarshalIndent(payload, "", "  ")
}

func renderSessionCSV(rows []sessionReportRow) ([]byte, error) {
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	headers := []string{"id", "user_id", "username", "machine_id", "machine", "remote_user", "source", "start_time", "end_time", "status"}
	if err := w.Write(headers); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.ID, r.UserID, r.Username, r.MachineID, r.Machine, r.RemoteUser, r.Source, r.StartTime, r.EndTime, r.Status}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderAuditCSV(rows []auditReportRow) ([]byte, error) {
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	headers := []string{"id", "user_id", "username", "action", "resource", "ip_address", "timestamp", "metadata"}
	if err := w.Write(headers); err != nil {
		return nil, err
	}
	for _, r := range rows {
		metadata, _ := json.Marshal(r.Metadata)
		if err := w.Write([]string{r.ID, r.UserID, r.Username, r.Action, r.Resource, r.IPAddress, r.Timestamp, string(metadata)}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderSessionPDF(rows []sessionReportRow, filters map[string]string, generatedAt time.Time) ([]byte, error) {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "B", 14)
	pdf.Cell(0, 8, "Orion Belt Session Report")
	pdf.Ln(9)

	pdf.SetFont("Arial", "", 9)
	pdf.Cell(0, 6, fmt.Sprintf("Generated at: %s UTC", generatedAt.Format("2006-01-02 15:04:05")))
	pdf.Ln(5)
	if len(filters) > 0 {
		pdf.Cell(0, 6, "Filters: "+formatFilters(filters))
		pdf.Ln(5)
	}
	pdf.Cell(0, 6, fmt.Sprintf("Records: %d", len(rows)))
	pdf.Ln(8)

	headers := []string{"Session ID", "User", "Machine", "Remote", "Source", "Started", "Ended", "Status"}
	widths := []float64{50, 30, 30, 22, 18, 45, 45, 20}
	writePDFHeader(pdf, headers, widths)
	pdf.SetFont("Arial", "", 8)

	for _, r := range rows {
		if pdf.GetY() > 190 {
			pdf.AddPage()
			writePDFHeader(pdf, headers, widths)
			pdf.SetFont("Arial", "", 8)
		}
		vals := []string{
			truncateForPDF(r.ID, 36),
			truncateForPDF(r.Username, 16),
			truncateForPDF(r.Machine, 16),
			truncateForPDF(r.RemoteUser, 12),
			truncateForPDF(r.Source, 10),
			truncateForPDF(r.StartTime, 24),
			truncateForPDF(r.EndTime, 24),
			truncateForPDF(r.Status, 12),
		}
		for i, v := range vals {
			pdf.CellFormat(widths[i], 6, v, "1", 0, "L", false, 0, "")
		}
		pdf.Ln(-1)
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func renderAuditPDF(rows []auditReportRow, filters map[string]string, generatedAt time.Time) ([]byte, error) {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "B", 14)
	pdf.Cell(0, 8, "Orion Belt Audit Report")
	pdf.Ln(9)

	pdf.SetFont("Arial", "", 9)
	pdf.Cell(0, 6, fmt.Sprintf("Generated at: %s UTC", generatedAt.Format("2006-01-02 15:04:05")))
	pdf.Ln(5)
	if len(filters) > 0 {
		pdf.Cell(0, 6, "Filters: "+formatFilters(filters))
		pdf.Ln(5)
	}
	pdf.Cell(0, 6, fmt.Sprintf("Records: %d", len(rows)))
	pdf.Ln(8)

	headers := []string{"Timestamp", "User", "Action", "Resource", "IP", "Metadata"}
	widths := []float64{36, 28, 45, 58, 28, 90}
	writePDFHeader(pdf, headers, widths)
	pdf.SetFont("Arial", "", 8)

	for _, r := range rows {
		if pdf.GetY() > 190 {
			pdf.AddPage()
			writePDFHeader(pdf, headers, widths)
			pdf.SetFont("Arial", "", 8)
		}
		metaJSON, _ := json.Marshal(r.Metadata)
		vals := []string{
			truncateForPDF(r.Timestamp, 20),
			truncateForPDF(r.Username, 14),
			truncateForPDF(r.Action, 24),
			truncateForPDF(r.Resource, 30),
			truncateForPDF(r.IPAddress, 20),
			truncateForPDF(string(metaJSON), 64),
		}
		for i, v := range vals {
			pdf.CellFormat(widths[i], 6, v, "1", 0, "L", false, 0, "")
		}
		pdf.Ln(-1)
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writePDFHeader(pdf *fpdf.Fpdf, headers []string, widths []float64) {
	pdf.SetFont("Arial", "B", 9)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 7, h, "1", 0, "L", false, 0, "")
	}
	pdf.Ln(-1)
}

func truncateForPDF(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func formatFilters(filters map[string]string) string {
	if len(filters) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(filters))
	for _, key := range []string{"status", "action", "actor", "q", "limit"} {
		if v, ok := filters[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", key, v))
		}
	}
	if len(parts) == 0 {
		for k, v := range filters {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return strings.Join(parts, ", ")
}
