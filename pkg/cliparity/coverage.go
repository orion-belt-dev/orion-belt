package cliparity

import (
	"sort"
	"strings"
)

// Cover records how the client CLIs relate to one API route.
//
// Exactly one of Command or Exempt must be set: either a CLI command exposes
// the endpoint, or there is a stated reason it does not.
type Cover struct {
	// Command is the CLI invocation that calls the endpoint, e.g.
	// "oadmin users list". Flags may be included when the coverage is a flag
	// rather than a subcommand ("osh --list"); the per-binary tests check
	// that both the command path and any named flags actually exist.
	Command string

	// Exempt explains why no CLI command covers the endpoint.
	Exempt string

	// Note adds context that is worth keeping next to the mapping.
	Note string
}

// Binary returns the CLI a covered route belongs to ("osh", "ocp", "oadmin"),
// or "" when the route is exempt.
func (c Cover) Binary() string {
	if c.Command == "" {
		return ""
	}
	return strings.Fields(c.Command)[0]
}

// Coverage maps "METHOD /path" (see Route.Key) to its CLI coverage.
//
// Adding a route to pkg/api without adding it here fails TestEveryRouteIsClassified.
// That is deliberate: it is the checkpoint where somebody decides whether the
// new surface needs a CLI command.
var Coverage = map[string]Cover{
	// --- Operator health & discovery -------------------------------------
	"GET /health":              {Exempt: "liveness probe for load balancers and container runtimes"},
	"GET /metrics":             {Exempt: "Prometheus scrape endpoint"},
	"GET /api/v1/version":      {Command: "oadmin version"},
	"GET /api/v1/gateway-info": {Command: "oadmin gateway-info"},
	"GET /api/v1/openapi.json": {Exempt: "OpenAPI document served for API tooling and the console"},
	"GET /api/v1/openapi.yaml": {Exempt: "OpenAPI document served for API tooling and the console"},

	// --- Login & registration --------------------------------------------
	"POST /api/v1/public/auth/challenge":                {Command: "osh login", Note: "every CLI signs this challenge to obtain an API key"},
	"POST /api/v1/public/login/key":                     {Command: "osh login"},
	"POST /api/v1/public/login/password":                {Command: "osh login --password"},
	"POST /api/v1/auth/browser-bootstrap":               {Command: "osh login"},
	"POST /api/v1/public/login":                         {Exempt: "legacy public-key login superseded by /public/login/key, which the CLIs use"},
	"POST /api/v1/public/login/token":                   {Exempt: "JWT login for SDK and agent integrations, not an interactive CLI flow"},
	"POST /api/v1/public/register/agent":                {Exempt: "agent self-registration; operators use the server CLI (orion-belt agent register)"},
	"POST /api/v1/public/register/client":               {Exempt: "console self-service signup; operators use oadmin users create"},
	"POST /api/v1/public/auth/browser-bootstrap/redeem": {Exempt: "redeemed by the browser after osh login opens it"},
	"POST /api/v1/logout":                               {Exempt: "invalidates a browser session token; CLI credentials are API keys, revoked with osh api-keys revoke"},

	// --- Current user ------------------------------------------------------
	"GET /api/v1/auth/me": {Command: "osh whoami"},

	// --- API keys (user-owned automation credentials) ----------------------
	"GET /api/v1/api-keys":             {Command: "osh api-keys list"},
	"POST /api/v1/api-keys":            {Command: "osh api-keys create"},
	"POST /api/v1/api-keys/:id/revoke": {Command: "osh api-keys revoke"},
	"DELETE /api/v1/api-keys/:id":      {Command: "osh api-keys delete"},

	// --- SSH keys ----------------------------------------------------------
	"GET /api/v1/ssh-keys":        {Command: "osh keys list"},
	"POST /api/v1/ssh-keys":       {Command: "osh keys add"},
	"DELETE /api/v1/ssh-keys/:id": {Command: "osh keys remove"},

	// --- MFA / password ----------------------------------------------------
	"GET /api/v1/mfa/status":       {Command: "osh mfa status"},
	"POST /api/v1/mfa/enroll":      {Command: "osh mfa enroll"},
	"POST /api/v1/mfa/confirm":     {Command: "osh mfa enroll", Note: "enrollment confirms the first code in the same command"},
	"POST /api/v1/mfa/disable":     {Command: "osh mfa disable"},
	"POST /api/v1/auth/password":   {Command: "osh password set"},
	"DELETE /api/v1/auth/password": {Command: "osh password clear"},

	// --- WebAuthn ----------------------------------------------------------
	"GET /api/v1/webauthn/credentials":          {Command: "osh webauthn list"},
	"DELETE /api/v1/webauthn/credentials/:id":   {Command: "osh webauthn remove"},
	"POST /api/v1/webauthn/register/begin":      {Exempt: "browser attestation ceremony; the CLI can list and remove credentials but not create them"},
	"POST /api/v1/webauthn/register/finish":     {Exempt: "browser attestation ceremony; the CLI can list and remove credentials but not create them"},
	"POST /api/v1/public/webauthn/login/begin":  {Exempt: "browser assertion ceremony; CLI logins use SSH keys or password+TOTP"},
	"POST /api/v1/public/webauthn/login/finish": {Exempt: "browser assertion ceremony; CLI logins use SSH keys or password+TOTP"},

	// --- Machines ----------------------------------------------------------
	"GET /api/v1/machines":              {Command: "osh --list", Note: "also oadmin machines list"},
	"GET /api/v1/machines/:id":          {Command: "oadmin machines get"},
	"POST /api/v1/admin/machines":       {Command: "oadmin machines create"},
	"PUT /api/v1/admin/machines/:id":    {Command: "oadmin machines update"},
	"DELETE /api/v1/admin/machines/:id": {Command: "oadmin machines delete"},

	// --- Users -------------------------------------------------------------
	"GET /api/v1/users":              {Command: "oadmin users list"},
	"GET /api/v1/users/:id":          {Command: "oadmin users get"},
	"POST /api/v1/admin/users":       {Command: "oadmin users create"},
	"PUT /api/v1/admin/users/:id":    {Command: "oadmin users update"},
	"DELETE /api/v1/admin/users/:id": {Command: "oadmin users delete"},

	// --- Permissions -------------------------------------------------------
	"GET /api/v1/permissions/user/:id":     {Command: "oadmin permissions list --user"},
	"GET /api/v1/permissions/machine/:id":  {Command: "oadmin permissions list --machine"},
	"GET /api/v1/admin/permissions":        {Command: "oadmin permissions list"},
	"POST /api/v1/admin/permissions":       {Command: "oadmin permissions grant"},
	"PATCH /api/v1/admin/permissions/:id":  {Command: "oadmin permissions update"},
	"DELETE /api/v1/admin/permissions/:id": {Command: "oadmin permissions revoke"},

	// --- Access requests ---------------------------------------------------
	"POST /api/v1/access-requests":                   {Command: "osh --request-access"},
	"GET /api/v1/access-requests":                    {Command: "osh requests list"},
	"GET /api/v1/access-requests/:id":                {Command: "osh requests get"},
	"GET /api/v1/admin/access-requests/pending":      {Command: "oadmin requests list"},
	"POST /api/v1/admin/access-requests/:id/approve": {Command: "oadmin requests approve"},
	"POST /api/v1/admin/access-requests/:id/reject":  {Command: "oadmin requests reject"},

	// --- Sessions ----------------------------------------------------------
	"GET /api/v1/sessions":             {Command: "oadmin sessions list"},
	"GET /api/v1/sessions/active":      {Command: "oadmin sessions list --active"},
	"GET /api/v1/sessions/:id":         {Command: "oadmin sessions get"},
	"GET /api/v1/sessions/:id/content": {Command: "oadmin sessions content"},
	"GET /api/v1/sessions/:id/watch":   {Exempt: "websocket live-watch stream consumed by the console player"},
	"GET /api/v1/terminal/ws":          {Exempt: "websocket web-terminal transport; the CLI equivalent is osh itself"},

	// --- Audit, reporting, usage ------------------------------------------
	"GET /api/v1/audit-logs":           {Command: "oadmin audit list"},
	"GET /api/v1/reports/:name/export": {Command: "oadmin reports export"},
	"GET /api/v1/dashboard/usage":      {Command: "oadmin usage"},
	"GET /api/v1/setup/status":         {Command: "oadmin setup status"},

	// --- Notifications -----------------------------------------------------
	"GET /api/v1/notifications":                           {Command: "osh notifications list"},
	"GET /api/v1/notifications/unread-count":              {Command: "osh notifications list", Note: "the unread count is printed in the list summary"},
	"POST /api/v1/notifications/:id/read":                 {Command: "osh notifications read"},
	"POST /api/v1/notifications/read-all":                 {Command: "osh notifications read --all"},
	"GET /api/v1/notifications/prefs":                     {Command: "osh notifications prefs"},
	"PUT /api/v1/notifications/prefs":                     {Command: "osh notifications prefs --set"},
	"GET /api/v1/admin/notifications/policy":              {Command: "oadmin notifications policy"},
	"PUT /api/v1/admin/notifications/policy":              {Command: "oadmin notifications set-policy"},
	"GET /api/v1/admin/notifications/templates":           {Command: "oadmin notifications templates"},
	"PUT /api/v1/admin/notifications/templates/:event":    {Command: "oadmin notifications set-template"},
	"DELETE /api/v1/admin/notifications/templates/:event": {Command: "oadmin notifications reset-template"},

	// --- Plugins -----------------------------------------------------------
	"GET /api/v1/admin/plugins":                   {Command: "oadmin plugins list"},
	"PUT /api/v1/admin/plugins/:name/config":      {Command: "oadmin plugins config"},
	"POST /api/v1/admin/plugins/:name/enable":     {Command: "oadmin plugins enable"},
	"POST /api/v1/admin/plugins/:name/disable":    {Command: "oadmin plugins disable"},
	"Any /api/v1/public/plugins/:name/*proxyPath": {Exempt: "inbound webhook target for chat platforms; each plugin verifies its own caller"},

	// --- Agents ------------------------------------------------------------
	"GET /api/v1/admin/agents/connected":               {Command: "oadmin agents list"},
	"POST /api/v1/admin/agents/:machine_id/command":    {Command: "oadmin agents command"},
	"POST /api/v1/admin/agents/:machine_id/disconnect": {Command: "oadmin agents disconnect"},
	"POST /api/v1/admin/agents/install-script":         {Command: "oadmin agents install-script"},

	// --- SSH certificate authority ----------------------------------------
	"GET /api/v1/ssh-cert/ca":                            {Command: "oadmin ca export", Note: "osh/ocp also call it to auto-detect CA mode when connecting"},
	"POST /api/v1/ssh-cert":                              {Exempt: "short-lived user certificates are issued transparently while osh/ocp connect"},
	"GET /api/v1/admin/ca/export":                        {Command: "oadmin ca export"},
	"GET /api/v1/admin/ssh-certificates":                 {Command: "oadmin ca list-certs"},
	"POST /api/v1/admin/ssh-certificates/:serial/revoke": {Command: "oadmin ca revoke"},

	// --- Remote file browser ----------------------------------------------
	"GET /api/v1/files/list":     {Command: "ocp ls"},
	"POST /api/v1/files/mkdir":   {Command: "ocp mkdir"},
	"DELETE /api/v1/files":       {Command: "ocp rm"},
	"GET /api/v1/files/download": {Command: "ocp ls", Note: "plain copies use SCP over the tunnel (ocp remote:path local); this endpoint backs the console file browser"},
	"POST /api/v1/files/upload":  {Command: "ocp ls", Note: "plain copies use SCP over the tunnel (ocp local remote:path); this endpoint backs the console file browser"},
}

// CommandsFor returns the sorted, de-duplicated CLI invocations Coverage
// records for one binary.
func CommandsFor(binary string) []string {
	seen := map[string]bool{}
	var out []string
	for _, cover := range Coverage {
		if cover.Binary() != binary || seen[cover.Command] {
			continue
		}
		seen[cover.Command] = true
		out = append(out, cover.Command)
	}
	sort.Strings(out)
	return out
}
