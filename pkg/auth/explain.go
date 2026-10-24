package auth

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// explainRequestHistory bounds how many of the user's access requests are
// scanned for ones about the machine being explained.
const explainRequestHistory = 50

// ExplainAccess says whether userID may open accessType as remoteUser on
// machineID and lists the relationships behind that answer. The decision is
// taken by CheckPermissionWithRemoteUser, the same call the gateway makes, so
// the explanation can never disagree with enforcement; the chain only
// describes it.
func (a *AuthService) ExplainAccess(ctx context.Context, userID, machineID, accessType, remoteUser string) (*common.AccessExplanation, error) {
	user, err := a.store.GetUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user %s: %w", userID, err)
	}
	machine, err := a.store.GetMachine(ctx, machineID)
	if err != nil {
		return nil, fmt.Errorf("get machine %s: %w", machineID, err)
	}
	ex := &common.AccessExplanation{
		UserID: user.ID, Username: user.Username,
		MachineID: machine.ID, Machine: machine.Name,
		AccessType: accessType, RemoteUser: remoteUser,
	}
	ex.Allowed = a.CheckPermissionWithRemoteUser(ctx, userID, machineID, accessType, remoteUser) == nil

	if privilegedAccess(user) {
		ex.Chain = append(ex.Chain, common.AccessLink{
			Relation: common.LinkRole, Ref: "role:" + user.EffectiveRole(), Holds: true,
			Detail: fmt.Sprintf("%s has the %s role, which reaches every machine as any account", user.Username, user.EffectiveRole()),
		})
	}

	grants, err := a.store.ListPairPermissions(ctx, userID, machineID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	requests, err := a.store.ListUserAccessRequests(ctx, userID, explainRequestHistory, 0)
	if err != nil {
		return nil, fmt.Errorf("list access requests: %w", err)
	}
	now := time.Now().UTC()
	for _, g := range grants {
		ex.Chain = append(ex.Chain, a.grantLink(ctx, g, accessType, remoteUser, requests, now))
	}
	for _, r := range requests {
		if r.MachineID != machineID || r.Status == "approved" {
			continue // approved requests are shown through the grant they created
		}
		ex.Chain = append(ex.Chain, requestLink(r, remoteUser))
	}
	if a.authorizer != nil {
		ex.Chain = append(ex.Chain, common.AccessLink{
			Relation: common.LinkOpenFGA,
			Detail:   "OpenFGA is consulted for machine access; the remote account is still checked against the grants above",
		})
	}
	if len(ex.Chain) == 0 {
		ex.Chain = append(ex.Chain, common.AccessLink{
			Relation: common.LinkNone,
			Detail:   fmt.Sprintf("%s has no grant on %s and has not requested access", user.Username, machine.Name),
		})
	}
	ex.Summary = summarize(ex)
	return ex, nil
}

func (a *AuthService) grantLink(ctx context.Context, g *common.Permission, accessType, remoteUser string, requests []*common.AccessRequest, now time.Time) common.AccessLink {
	link := common.AccessLink{
		Relation:  common.LinkGrant,
		Ref:       "permission:" + g.ID,
		GrantedBy: a.username(ctx, g.GrantedBy),
		At:        ptrTime(common.FromDBTimestamp(g.GrantedAt)),
	}
	if g.ExpiresAt != nil {
		link.ExpiresAt = ptrTime(common.FromDBTimestamp(*g.ExpiresAt))
	}
	if r := sourceRequest(g, requests); r != nil {
		link.Via = "access_request:" + r.ID
		link.Reason = r.Reason
		if r.ReviewedBy == nil {
			link.GrantedBy = "risk policy"
		}
	}

	switch {
	case link.ExpiresAt != nil && !link.ExpiresAt.After(now):
		link.Detail = fmt.Sprintf("grant expired at %s", link.ExpiresAt.Format(time.RFC3339))
	case !common.AccessTypeCovers(g.AccessType, accessType):
		link.Detail = fmt.Sprintf("grant is for %s, not %s", g.AccessType, accessType)
	case !common.IsTunnelAccessType(accessType) && !slices.Contains(g.RemoteUsers, remoteUser):
		link.Detail = fmt.Sprintf("grant allows %s, not %s", listOrNone(g.RemoteUsers), remoteUser)
	default:
		link.Holds = true
		link.Detail = fmt.Sprintf("grant allows %s as %s", accessType, listOrNone(g.RemoteUsers))
		if link.ExpiresAt != nil {
			link.Detail += " until " + link.ExpiresAt.Format(time.RFC3339)
		}
	}
	if link.GrantedBy != "" {
		link.Detail += ", granted by " + link.GrantedBy
	}
	return link
}

// sourceRequest finds the approved request that created a grant: same pair,
// approved by the grantor, and reviewed when the grant was written.
func sourceRequest(g *common.Permission, requests []*common.AccessRequest) *common.AccessRequest {
	for _, r := range requests {
		if r.Status != "approved" || r.MachineID != g.MachineID || r.ReviewedAt == nil {
			continue
		}
		reviewer := "" // approved by the risk policy
		if r.ReviewedBy != nil {
			reviewer = *r.ReviewedBy
		}
		if reviewer != g.GrantedBy {
			continue
		}
		if d := r.ReviewedAt.Sub(g.GrantedAt); d > -5*time.Second && d < 5*time.Second {
			return r
		}
	}
	return nil
}

func requestLink(r *common.AccessRequest, remoteUser string) common.AccessLink {
	link := common.AccessLink{
		Relation: common.LinkAccessRequest,
		Ref:      "access_request:" + r.ID,
		Reason:   r.Reason,
		At:       ptrTime(common.FromDBTimestamp(r.RequestedAt)),
	}
	switch r.Status {
	case "pending":
		link.Detail = "request for " + listOrNone(r.RemoteUsers) + " is waiting for approval"
	case "rejected":
		link.Detail = "request for " + listOrNone(r.RemoteUsers) + " was rejected"
	default:
		link.Detail = "request for " + listOrNone(r.RemoteUsers) + " is " + r.Status
	}
	if !slices.Contains(r.RemoteUsers, remoteUser) {
		link.Detail += " (it does not cover " + remoteUser + ")"
	}
	return link
}

// summarize puts the decisive link in one sentence for inline display.
func summarize(ex *common.AccessExplanation) string {
	target := ex.RemoteUser + "@" + ex.Machine
	if ex.Allowed {
		for _, l := range ex.Chain {
			if l.Holds {
				return fmt.Sprintf("Allowed: %s may reach %s because %s.", ex.Username, target, l.Detail)
			}
		}
		return fmt.Sprintf("Allowed: OpenFGA authorizes %s on %s.", ex.Username, target)
	}
	for _, l := range ex.Chain {
		if l.Relation == common.LinkGrant && l.Holds {
			return fmt.Sprintf("Denied: OpenFGA refuses %s on %s although %s.", ex.Username, target, l.Detail)
		}
	}
	for _, l := range ex.Chain {
		if l.Relation == common.LinkAccessRequest && strings.Contains(l.Detail, "waiting for approval") {
			return fmt.Sprintf("Denied: %s cannot reach %s yet; %s (%s).", ex.Username, target, l.Detail, l.Ref)
		}
	}
	for _, l := range ex.Chain {
		if l.Relation == common.LinkGrant {
			return fmt.Sprintf("Denied: %s cannot reach %s; %s.", ex.Username, target, l.Detail)
		}
	}
	return fmt.Sprintf("Denied: %s has no grant on %s. Request access with: osh -r %s --reason \"...\"", ex.Username, ex.Machine, target)
}

func (a *AuthService) username(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if u, err := a.store.GetUser(ctx, id); err == nil {
		return u.Username
	}
	return id
}

func listOrNone(v []string) string {
	if len(v) == 0 {
		return "no account"
	}
	return strings.Join(v, ", ")
}

func ptrTime(t time.Time) *time.Time { return &t }
