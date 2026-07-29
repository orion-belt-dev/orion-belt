package notify

import (
	"fmt"
	"strings"
	"time"
)

// Notification event types. These are the values that appear in a user's
// event allow-list and in an admin policy's mandatory-event map.
const (
	EventAccessRequestApproved = "access_request.approved"
	EventAccessRequestRejected = "access_request.rejected"
	EventAccessRequestExpired  = "access_request.expired"
)

// KnownEventTypes lists the event types this build renders, in stable order,
// so the admin UI can offer a picker instead of a free-text field. Delivery is
// not restricted to this list — an unrecognized type still renders via the
// default branch of Render — but anything outside it has no designed copy.
func KnownEventTypes() []string {
	return []string{
		EventAccessRequestApproved,
		EventAccessRequestRejected,
		EventAccessRequestExpired,
	}
}

// Render builds title/body for a known notification type.
func Render(notifType string, data map[string]string) (title, body string) {
	machine := data["machine"]
	if machine == "" {
		machine = data["machine_id"]
	}
	remote := data["remote_users"]
	if remote == "" {
		remote = "as allowed"
	} else if !strings.HasPrefix(remote, "as ") {
		remote = "as " + remote
	}
	ttl := data["ttl"]
	if ttl == "" {
		ttl = "unlimited"
	}

	switch notifType {
	case "access_request.approved":
		return "Access request approved",
			fmt.Sprintf("Your access request for %s (%s) was approved — access %s.", machine, remote, ttl)
	case "access_request.rejected":
		return "Access request rejected",
			fmt.Sprintf("Your access request for %s (%s) was rejected.", machine, remote)
	case "access_request.expired":
		return "Access request expired",
			fmt.Sprintf("Your pending access request for %s expired without approval.", machine)
	default:
		return notifType, data["body"]
	}
}

// FormatTTL formats an expiry for notification copy.
func FormatTTL(expiresAt *time.Time) string {
	if expiresAt == nil {
		return "unlimited"
	}
	return "until " + expiresAt.Format(time.RFC3339)
}
