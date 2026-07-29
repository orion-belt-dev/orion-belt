import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useToast } from "../components/Toast";
import { useAuth } from "../auth/AuthContext";
import { fmtTime, preparePublicKeyCreation, publicKeyCredentialToJSON } from "../lib/format";

type SSHKey = { id: string; name: string; key_type?: string; public_key?: string; created_at?: string };
type WACred = { id: string; name: string; cred_id?: string; created_at?: string };
type APIKeyItem = {
  id: string;
  name: string;
  key_prefix?: string;
  last_used_at?: string;
  expires_at?: string;
  created_at?: string;
  revoked_at?: string;
};

export function SecurityPage() {
  const { toast } = useToast();
  const { refreshMe, user } = useAuth();
  const qc = useQueryClient();
  const [tab, setTab] = useState<"mfa" | "password" | "webauthn" | "keys" | "api-keys" | "notifications">("mfa");
  const [notifInApp, setNotifInApp] = useState(true);
  const [notifEmail, setNotifEmail] = useState(false);
  const [notifEvents, setNotifEvents] = useState("");
  const [otpauth, setOtpauth] = useState("");
  const [secret, setSecret] = useState("");
  const [backupCodes, setBackupCodes] = useState<string[]>([]);
  const [code, setCode] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordTotp, setPasswordTotp] = useState("");
  const [keyName, setKeyName] = useState("");
  const [keyPub, setKeyPub] = useState("");
  const [waErr, setWaErr] = useState("");
  const [apiKeyName, setApiKeyName] = useState("");
  const [apiKeyExpiresDays, setApiKeyExpiresDays] = useState(0);
  const [newAPIKey, setNewAPIKey] = useState("");

  const mfaStatus = useQuery({
    queryKey: ["mfa", "status"],
    queryFn: () => api<{ mfa_enabled?: boolean; mfa_required?: boolean }>("/mfa/status"),
    enabled: tab === "mfa",
  });
  const keys = useQuery({
    queryKey: ["ssh-keys"],
    queryFn: () => api<SSHKey[]>("/ssh-keys"),
    enabled: tab === "keys",
  });
  const waCreds = useQuery({
    queryKey: ["webauthn", "credentials"],
    queryFn: () => api<WACred[]>("/webauthn/credentials"),
    enabled: tab === "webauthn",
    retry: false,
  });
  const apiKeys = useQuery({
    queryKey: ["api-keys"],
    queryFn: () => api<{ api_keys?: APIKeyItem[] }>("/api-keys"),
    enabled: tab === "api-keys",
  });
  const notifPrefs = useQuery({
    queryKey: ["notification-prefs"],
    queryFn: () =>
      api<{
        in_app_enabled?: boolean;
        email_enabled?: boolean;
        event_types?: string[];
        allowed_channels?: string[];
        mandatory_events?: Record<string, string[]>;
      }>("/notifications/prefs"),
    enabled: tab === "notifications",
  });

  // Admin bounds. Channels absent from allowed_channels can't be enabled;
  // events listed in mandatory_events are always delivered and are shown
  // locked rather than hidden, so the user can see why.
  const allowedChannels = notifPrefs.data?.allowed_channels ?? ["in_app", "email"];
  const mandatoryEvents = notifPrefs.data?.mandatory_events ?? {};
  const inAppAllowed = allowedChannels.includes("in_app");
  const emailAllowed = allowedChannels.includes("email");
  const mandatoryInApp = mandatoryEvents["in_app"] ?? [];
  const mandatoryEmail = mandatoryEvents["email"] ?? [];

  useEffect(() => {
    if (!notifPrefs.data) return;
    setNotifInApp(notifPrefs.data.in_app_enabled !== false);
    setNotifEmail(!!notifPrefs.data.email_enabled);
    setNotifEvents((notifPrefs.data.event_types || []).join(", "));
  }, [notifPrefs.data]);

  async function enrollMfa() {
    try {
      const data = await api<{ otpauth_url?: string; secret?: string; backup_codes?: string[] }>("/mfa/enroll", {
        method: "POST",
        body: "{}",
      });
      setOtpauth(data.otpauth_url || "");
      setSecret(data.secret || "");
      setBackupCodes(data.backup_codes || []);
      toast("MFA enrollment started — scan the QR code");
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function confirmMfa(e: FormEvent) {
    e.preventDefault();
    try {
      await api("/mfa/confirm", { method: "POST", body: JSON.stringify({ code }) });
      toast("MFA enabled");
      setCode("");
      setOtpauth("");
      setSecret("");
      setBackupCodes([]);
      await refreshMe();
      void qc.invalidateQueries({ queryKey: ["mfa"] });
    } catch (ex) {
      toast(ex instanceof Error ? ex.message : String(ex), "err");
    }
  }

  async function disableMfa() {
    if (user?.password_set) {
      toast("Clear your password first before disabling MFA", "err");
      return;
    }
    const c = prompt("Enter TOTP or backup code to disable MFA");
    if (!c) return;
    try {
      await api("/mfa/disable", { method: "POST", body: JSON.stringify({ code: c }) });
      toast("MFA disabled");
      await refreshMe();
      void qc.invalidateQueries({ queryKey: ["mfa"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function setAccountPassword(e: FormEvent) {
    e.preventDefault();
    if (newPassword.length < 10) {
      toast("Password must be at least 10 characters", "err");
      return;
    }
    if (newPassword !== confirmPassword) {
      toast("Passwords do not match", "err");
      return;
    }
    try {
      if (!(user?.mfa_enabled || mfaStatus.data?.mfa_enabled) && !secret) {
        const data = await api<{ otpauth_url?: string; secret?: string; backup_codes?: string[] }>("/mfa/enroll", {
          method: "POST",
          body: "{}",
        });
        setOtpauth(data.otpauth_url || "");
        setSecret(data.secret || "");
        setBackupCodes(data.backup_codes || []);
        toast("Scan the QR, then submit again with a TOTP code");
        return;
      }
      await api("/auth/password", {
        method: "POST",
        body: JSON.stringify({ password: newPassword, totp_code: passwordTotp.trim() }),
      });
      toast(user?.password_set ? "Password updated" : "Password set");
      setNewPassword("");
      setConfirmPassword("");
      setPasswordTotp("");
      setOtpauth("");
      setSecret("");
      setBackupCodes([]);
      await refreshMe();
      void qc.invalidateQueries({ queryKey: ["mfa"] });
    } catch (ex) {
      toast(ex instanceof Error ? ex.message : String(ex), "err");
    }
  }

  async function clearAccountPassword() {
    const c = prompt("Enter TOTP code to clear password login");
    if (!c) return;
    try {
      await api("/auth/password", { method: "DELETE", body: JSON.stringify({ totp_code: c }) });
      toast("Password cleared");
      await refreshMe();
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function registerWebAuthn() {
    setWaErr("");
    try {
      const begin = await api<{ publicKey?: Record<string, unknown> } | Record<string, unknown>>(
        "/webauthn/register/begin",
        { method: "POST", body: "{}" },
      );
      const publicKey = preparePublicKeyCreation(
        (("publicKey" in begin && begin.publicKey) || begin) as Record<string, unknown>,
      );
      const cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null;
      if (!cred) throw new Error("registration cancelled");
      await api("/webauthn/register/finish", {
        method: "POST",
        body: JSON.stringify(publicKeyCredentialToJSON(cred)),
      });
      toast("Security key registered");
      await refreshMe();
      void qc.invalidateQueries({ queryKey: ["webauthn"] });
    } catch (e) {
      setWaErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function deleteWA(id: string) {
    if (!confirm("Remove this authenticator?")) return;
    try {
      await api(`/webauthn/credentials/${encodeURIComponent(id)}`, { method: "DELETE" });
      toast("Credential removed");
      void qc.invalidateQueries({ queryKey: ["webauthn"] });
      await refreshMe();
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function addKey(e: FormEvent) {
    e.preventDefault();
    try {
      await api("/ssh-keys", {
        method: "POST",
        body: JSON.stringify({ name: keyName.trim(), public_key: keyPub.trim() }),
      });
      toast("SSH key added");
      setKeyName("");
      setKeyPub("");
      void qc.invalidateQueries({ queryKey: ["ssh-keys"] });
    } catch (ex) {
      toast(ex instanceof Error ? ex.message : String(ex), "err");
    }
  }

  async function deleteKey(id: string) {
    if (!confirm("Delete this SSH key?")) return;
    try {
      await api(`/ssh-keys/${encodeURIComponent(id)}`, { method: "DELETE" });
      toast("Key deleted");
      void qc.invalidateQueries({ queryKey: ["ssh-keys"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function createAPIKey(e: FormEvent) {
    e.preventDefault();
    try {
      const body: Record<string, unknown> = { name: apiKeyName.trim() };
      if (apiKeyExpiresDays > 0) body.expires_in = apiKeyExpiresDays;
      const res = await api<{ api_key: string }>("/api-keys", { method: "POST", body: JSON.stringify(body) });
      setNewAPIKey(res.api_key);
      setApiKeyName("");
      setApiKeyExpiresDays(0);
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    } catch (ex) {
      toast(ex instanceof Error ? ex.message : String(ex), "err");
    }
  }

  async function revokeAPIKey(id: string, name: string) {
    if (!confirm(`Revoke API key "${name}"? It will stop working immediately but stays in the audit trail.`)) return;
    try {
      await api(`/api-keys/${encodeURIComponent(id)}/revoke`, { method: "POST", body: "{}" });
      toast("API key revoked");
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function deleteAPIKey(id: string, name: string) {
    if (!confirm(`Permanently delete API key "${name}"? This cannot be undone.`)) return;
    try {
      await api(`/api-keys/${encodeURIComponent(id)}`, { method: "DELETE" });
      toast("API key deleted");
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function saveNotifPrefs(e: FormEvent) {
    e.preventDefault();
    try {
      const events = notifEvents
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      const saved = await api<{ adjusted?: boolean }>("/notifications/prefs", {
        method: "PUT",
        body: JSON.stringify({
          in_app_enabled: notifInApp,
          email_enabled: notifEmail,
          event_types: events,
        }),
      });
      // The server clamps preferences into the admin policy, so what was
      // stored can differ from what was submitted. Say so instead of showing
      // a plain success and letting the controls silently snap back.
      toast(
        saved.adjusted
          ? "Preferences saved, with some choices adjusted to your administrator's policy"
          : "Notification preferences saved",
      );
      void qc.invalidateQueries({ queryKey: ["notification-prefs"] });
    } catch (ex) {
      toast(ex instanceof Error ? ex.message : String(ex), "err");
    }
  }

  const keyList = keys.data || [];
  const qrSrc = otpauth
    ? `https://api.qrserver.com/v1/create-qr-code/?size=220x220&ecc=M&data=${encodeURIComponent(otpauth)}`
    : "";

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Security</h1>
          <p>MFA, passkeys, and keys for your account.</p>
        </div>
      </div>
      <div className="row" style={{ marginBottom: "1rem" }}>
        {(["mfa", "password", "webauthn", "keys", "api-keys", "notifications"] as const).map((t) => (
          <button key={t} type="button" className={`btn sm${tab === t ? "" : " secondary"}`} onClick={() => setTab(t)}>
            {t === "mfa"
              ? "MFA"
              : t === "password"
                ? "Password"
                : t === "webauthn"
                  ? "WebAuthn"
                  : t === "keys"
                    ? "SSH keys"
                    : t === "api-keys"
                      ? "API keys"
                      : "Notifications"}
          </button>
        ))}
      </div>

      {tab === "mfa" ? (
        <div className="card">
          <h3>Time-based one-time passwords</h3>
          <p className="muted">
            Status: {mfaStatus.data?.mfa_enabled || user?.mfa_enabled ? "enabled" : "disabled"}
            {mfaStatus.data?.mfa_required ? " · organization requires MFA" : ""}
            {user?.password_set ? " · required while a password is set" : ""}
          </p>
          <div className="row">
            <button type="button" className="btn sm" onClick={() => void enrollMfa()} disabled={!!(mfaStatus.data?.mfa_enabled || user?.mfa_enabled)}>
              Start enrollment
            </button>
            <button
              type="button"
              className="btn danger sm"
              onClick={() => void disableMfa()}
              disabled={!(mfaStatus.data?.mfa_enabled || user?.mfa_enabled) || !!user?.password_set}
              title={user?.password_set ? "Clear password first" : undefined}
            >
              Disable MFA
            </button>
          </div>
          {secret ? (
            <div className="mfa-enroll" style={{ marginTop: "1rem" }}>
              <p className="okmsg">Scan this QR code with your authenticator, then confirm with a code.</p>
              <div className="mfa-qr-row">
                {qrSrc ? (
                  <img className="mfa-qr" src={qrSrc} alt="TOTP QR code" width={220} height={220} />
                ) : null}
                <div>
                  <label className="field">Secret (manual entry)</label>
                  <input className="mono" readOnly value={secret} />
                  <label className="field">otpauth URL</label>
                  <textarea className="mono" rows={2} readOnly value={otpauth} />
                </div>
              </div>
              {backupCodes.length ? (
                <>
                  <label className="field">Backup codes — save these now</label>
                  <pre className="session">{backupCodes.join("\n")}</pre>
                </>
              ) : null}
              <form onSubmit={confirmMfa}>
                <label className="field">Confirm code</label>
                <input value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" />
                <button className="btn sm" type="submit" style={{ marginTop: "0.55rem" }}>
                  Confirm enrollment
                </button>
              </form>
            </div>
          ) : null}
        </div>
      ) : null}

      {tab === "password" ? (
        <div className="card">
          <h3>Password + TOTP login</h3>
          <p className="muted">
            Status: {user?.password_set ? "password set" : "no password"} · Console login always requires a TOTP
            code after the password.
          </p>
          <form onSubmit={(e) => void setAccountPassword(e)}>
            <label className="field">{user?.password_set ? "New password" : "Password"}</label>
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              autoComplete="new-password"
              minLength={10}
              required
            />
            <label className="field">Confirm</label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              autoComplete="new-password"
              minLength={10}
              required
            />
            {secret ? (
              <div className="mfa-enroll" style={{ margin: "0.75rem 0" }}>
                <p className="okmsg">Scan this authenticator QR, then enter a code below.</p>
                {otpauth ? (
                  <img
                    className="mfa-qr"
                    src={`https://api.qrserver.com/v1/create-qr-code/?size=180x180&ecc=M&data=${encodeURIComponent(otpauth)}`}
                    alt="TOTP QR"
                    width={180}
                    height={180}
                  />
                ) : null}
                <label className="field">Secret</label>
                <input className="mono" readOnly value={secret} />
                {backupCodes.length ? (
                  <>
                    <label className="field">Backup codes</label>
                    <pre className="session">{backupCodes.join("\n")}</pre>
                  </>
                ) : null}
              </div>
            ) : null}
            <label className="field">Authenticator code</label>
            <input
              value={passwordTotp}
              onChange={(e) => setPasswordTotp(e.target.value)}
              autoComplete="one-time-code"
              required
            />
            <div className="row" style={{ marginTop: "0.55rem" }}>
              <button className="btn sm" type="submit">
                {user?.password_set ? "Update password" : "Set password"}
              </button>
              {user?.password_set ? (
                <button type="button" className="btn danger sm" onClick={() => void clearAccountPassword()}>
                  Clear password
                </button>
              ) : null}
            </div>
          </form>
        </div>
      ) : null}

      {tab === "webauthn" ? (
        <>
          <div className="card">
            <h3>Registered authenticators</h3>
            <p className="muted">Status: {user?.webauthn_enabled ? "enabled" : "not configured"}</p>
            <div className="row" style={{ marginBottom: "0.85rem" }}>
              <button type="button" className="btn sm" onClick={() => void registerWebAuthn()}>
                Register YubiKey / FIDO2
              </button>
            </div>
            {waErr ? <div className="err">{waErr}</div> : null}
            {waCreds.isError ? (
              <div className="err">
                {(waCreds.error as Error).message}
                <p className="muted">WebAuthn may be disabled or misconfigured on this server.</p>
              </div>
            ) : null}
            {(waCreds.data || []).length ? (
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Credential</th>
                    <th>Created</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {(waCreds.data || []).map((c) => (
                    <tr key={c.id}>
                      <td>{c.name || "key"}</td>
                      <td className="mono">{(c.cred_id || c.id).slice(0, 16)}…</td>
                      <td className="mono">{fmtTime(c.created_at)}</td>
                      <td>
                        <button type="button" className="btn danger sm" onClick={() => void deleteWA(c.id)}>
                          Delete
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : !waCreds.isError ? (
              <div className="empty">No WebAuthn credentials yet. Register a key above (not from the login page).</div>
            ) : null}
          </div>
        </>
      ) : null}

      {tab === "keys" ? (
        <>
          <div className="card">
            <h3>Add SSH public key</h3>
            <p className="muted">
              Keys listed here are used for gateway SSH / API login. Your account’s legacy single{" "}
              <span className="mono">public_key</span> field is separate — add keys explicitly below (classic or FIDO{" "}
              <span className="mono">sk-*</span>).
            </p>
            <form onSubmit={addKey}>
              <label className="field">Name</label>
              <input value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder="laptop, yubikey-work…" required />
              <label className="field">Public key</label>
              <textarea
                rows={3}
                value={keyPub}
                onChange={(e) => setKeyPub(e.target.value)}
                placeholder="ssh-ed25519 AAAA… or sk-ssh-ed25519@openssh.com AAAA…"
                required
              />
              <button className="btn sm" type="submit" style={{ marginTop: "0.55rem" }}>
                Add key
              </button>
            </form>
          </div>
          <div className="card" style={{ marginTop: "1rem" }}>
            <h3>Your keys</h3>
            {keyList.length === 0 ? (
              <div className="empty">No keys in the SSH key store yet. Add one above to sign in with pubkey auth.</div>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Type</th>
                    <th>Created</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {keyList.map((k) => (
                    <tr key={k.id}>
                      <td>{k.name}</td>
                      <td className="mono">{k.key_type || "—"}</td>
                      <td className="mono">{fmtTime(k.created_at)}</td>
                      <td>
                        <button type="button" className="btn danger sm" onClick={() => void deleteKey(k.id)}>
                          Delete
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      ) : null}

      {tab === "api-keys" ? (
        <>
          <div className="card">
            <h3>Create API key</h3>
            <p className="muted">
              API keys authenticate CLI tools (<span className="mono">osh</span>) and scripts against the REST API. The
              raw key is shown once at creation — copy it now, it can't be retrieved again.
            </p>
            <form onSubmit={createAPIKey}>
              <label className="field">Name</label>
              <input value={apiKeyName} onChange={(e) => setApiKeyName(e.target.value)} placeholder="ci-pipeline, laptop-cli…" required />
              <label className="field">Expires in days (0 = never)</label>
              <input
                type="number"
                min={0}
                value={apiKeyExpiresDays}
                onChange={(e) => setApiKeyExpiresDays(Number(e.target.value) || 0)}
              />
              <button className="btn sm" type="submit" style={{ marginTop: "0.55rem" }}>
                Create key
              </button>
            </form>
            {newAPIKey ? (
              <div className="mfa-enroll" style={{ marginTop: "1rem" }}>
                <p className="okmsg">Copy this key now — it won't be shown again.</p>
                <div className="row">
                  <input className="mono" readOnly value={newAPIKey} style={{ flex: 1 }} />
                  <button
                    type="button"
                    className="btn secondary sm"
                    onClick={async () => {
                      await navigator.clipboard.writeText(newAPIKey);
                      toast("API key copied");
                    }}
                  >
                    Copy
                  </button>
                  <button type="button" className="btn secondary sm" onClick={() => setNewAPIKey("")}>
                    Dismiss
                  </button>
                </div>
              </div>
            ) : null}
          </div>
          <div className="card" style={{ marginTop: "1rem" }}>
            <h3>Your API keys</h3>
            {(apiKeys.data?.api_keys || []).length === 0 ? (
              <div className="empty">No API keys yet. Create one above to authenticate {"osh"} or scripts.</div>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Prefix</th>
                    <th>Created</th>
                    <th>Last used</th>
                    <th>Expires</th>
                    <th>Status</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {(apiKeys.data?.api_keys || []).map((k) => (
                    <tr key={k.id}>
                      <td>{k.name}</td>
                      <td className="mono">{k.key_prefix ? `${k.key_prefix}…` : "—"}</td>
                      <td className="mono">{fmtTime(k.created_at)}</td>
                      <td className="mono">{k.last_used_at ? fmtTime(k.last_used_at) : "never"}</td>
                      <td className="mono">{k.expires_at ? fmtTime(k.expires_at) : "never"}</td>
                      <td>{k.revoked_at ? <span className="muted">revoked</span> : <span className="okmsg">active</span>}</td>
                      <td>
                        <div className="row">
                          <button
                            type="button"
                            className="btn secondary sm"
                            disabled={!!k.revoked_at}
                            onClick={() => void revokeAPIKey(k.id, k.name)}
                          >
                            Revoke
                          </button>
                          <button type="button" className="btn danger sm" onClick={() => void deleteAPIKey(k.id, k.name)}>
                            Delete
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      ) : null}

      {tab === "notifications" ? (
        <form className="card" onSubmit={(e) => void saveNotifPrefs(e)}>
          <h3>Notification preferences</h3>
          <p className="muted">
            Control in-app delivery. Leave event types empty to receive all events (approve, reject, …). Email delivery
            is reserved for a future SMTP channel and is stored but not mailed yet.
          </p>
          {notifPrefs.isLoading ? (
            <p className="muted">Loading…</p>
          ) : (
            <>
              <div className="form-grid">
                <label
                  className="row"
                  style={{ alignItems: "center", gap: "0.5rem", opacity: inAppAllowed ? 1 : 0.55 }}
                  title={inAppAllowed ? undefined : "Disabled by your administrator's notification policy"}
                >
                  <input
                    type="checkbox"
                    checked={notifInApp}
                    disabled={!inAppAllowed || mandatoryInApp.length > 0}
                    onChange={(e) => setNotifInApp(e.target.checked)}
                  />
                  In-app notifications
                  {mandatoryInApp.length > 0 ? <span className="muted"> — required</span> : null}
                </label>
                <label
                  className="row"
                  style={{ alignItems: "center", gap: "0.5rem", opacity: emailAllowed ? 1 : 0.55 }}
                  title={emailAllowed ? undefined : "Disabled by your administrator's notification policy"}
                >
                  <input
                    type="checkbox"
                    checked={notifEmail}
                    disabled={!emailAllowed || mandatoryEmail.length > 0}
                    onChange={(e) => setNotifEmail(e.target.checked)}
                  />
                  Email (stored preference)
                  {mandatoryEmail.length > 0 ? <span className="muted"> — required</span> : null}
                </label>
                <div>
                  <label className="field">Event allow-list (comma-separated, empty = all)</label>
                  <input
                    value={notifEvents}
                    onChange={(e) => setNotifEvents(e.target.value)}
                    placeholder="access_request.approved, access_request.rejected"
                  />
                </div>
              </div>
              {mandatoryInApp.length > 0 || mandatoryEmail.length > 0 ? (
                <p className="muted" style={{ marginTop: "0.75rem" }}>
                  Your administrator requires these notifications; they are delivered even if you exclude them above.
                  {mandatoryInApp.length > 0 ? ` In-app: ${mandatoryInApp.join(", ")}.` : ""}
                  {mandatoryEmail.length > 0 ? ` Email: ${mandatoryEmail.join(", ")}.` : ""}
                </p>
              ) : null}
              {!inAppAllowed || !emailAllowed ? (
                <p className="muted" style={{ marginTop: "0.5rem" }}>
                  Some channels are unavailable under your administrator's notification policy.
                </p>
              ) : null}
              <button className="btn sm" type="submit" style={{ marginTop: "0.75rem" }}>
                Save preferences
              </button>
            </>
          )}
        </form>
      ) : null}
    </>
  );
}
