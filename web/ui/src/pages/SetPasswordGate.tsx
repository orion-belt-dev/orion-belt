import { useState } from "react";
import type { FormEvent } from "react";
import { api } from "../lib/api";
import { useAuth } from "../auth/AuthContext";
import { useToast } from "../components/Toast";

/**
 * Blocking post-login flow: users without a password must set one.
 * Setting a password also enrolls TOTP in the same step (API confirms both).
 */
export function SetPasswordGate() {
  const { refreshMe, logout, user } = useAuth();
  const { toast } = useToast();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [code, setCode] = useState("");
  const [otpauth, setOtpauth] = useState("");
  const [secret, setSecret] = useState("");
  const [backupCodes, setBackupCodes] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const mfaAlready = !!user?.mfa_enabled;

  async function startEnroll() {
    setError("");
    setBusy(true);
    try {
      const data = await api<{ otpauth_url?: string; secret?: string; backup_codes?: string[] }>("/mfa/enroll", {
        method: "POST",
        body: "{}",
      });
      setOtpauth(data.otpauth_url || "");
      setSecret(data.secret || "");
      setBackupCodes(data.backup_codes || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    if (password.length < 10) {
      setError("Password must be at least 10 characters");
      return;
    }
    if (password !== confirm) {
      setError("Passwords do not match");
      return;
    }
    if (!code.trim()) {
      setError("Enter a TOTP code from your authenticator");
      return;
    }
    if (!mfaAlready && !secret) {
      setError("Start authenticator enrollment first");
      return;
    }
    setBusy(true);
    try {
      await api("/auth/password", {
        method: "POST",
        body: JSON.stringify({ password, totp_code: code.trim() }),
      });
      toast("Password and authenticator saved");
      await refreshMe();
    } catch (ex) {
      setError(ex instanceof Error ? ex.message : String(ex));
    } finally {
      setBusy(false);
    }
  }

  const qrSrc = otpauth
    ? `https://api.qrserver.com/v1/create-qr-code/?size=200x200&ecc=M&data=${encodeURIComponent(otpauth)}`
    : "";

  return (
    <div className="login-stage">
      <form className="card login-panel" onSubmit={(e) => void onSubmit(e)}>
        <h1 className="login-brand">
          Orion <em>Belt</em>
        </h1>
        <p className="login-tag">Create a password to enable password + TOTP sign-in</p>
        <p className="muted" style={{ fontSize: "0.85rem", marginBottom: "1rem" }}>
          Signed in as <strong>{user?.username}</strong>. A password requires an authenticator app —
          enroll and confirm in one step.
        </p>

        {!mfaAlready && !secret ? (
          <button type="button" className="btn block" disabled={busy} onClick={() => void startEnroll()}>
            Start authenticator enrollment
          </button>
        ) : null}

        {secret ? (
          <div className="mfa-enroll" style={{ marginBottom: "1rem" }}>
            <p className="okmsg">Scan the QR code, then set your password below.</p>
            <div className="mfa-qr-row">
              {qrSrc ? <img className="mfa-qr" src={qrSrc} alt="TOTP QR code" width={200} height={200} /> : null}
              <div>
                <label className="field">Secret (manual)</label>
                <input className="mono" readOnly value={secret} />
              </div>
            </div>
            {backupCodes.length ? (
              <>
                <div className="row" style={{ justifyContent: "space-between", alignItems: "baseline" }}>
                  <label className="field">Backup codes — save these now</label>
                  <button
                    type="button"
                    className="btn secondary sm"
                    onClick={async () => {
                      await navigator.clipboard.writeText(backupCodes.join("\n"));
                      toast("Backup codes copied");
                    }}
                  >
                    Copy
                  </button>
                </div>
                <pre className="session">{backupCodes.join("\n")}</pre>
              </>
            ) : null}
          </div>
        ) : null}

        {(mfaAlready || secret) && (
          <>
            <label className="field">New password</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              minLength={10}
              required
            />
            <label className="field">Confirm password</label>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete="new-password"
              minLength={10}
              required
            />
            <label className="field">Authenticator code</label>
            <input
              value={code}
              onChange={(e) => setCode(e.target.value)}
              autoComplete="one-time-code"
              inputMode="numeric"
              required
            />
            <button className="btn block" type="submit" disabled={busy} style={{ marginTop: "0.85rem" }}>
              Save password
            </button>
          </>
        )}

        {error ? <div className="err">{error}</div> : null}
        <button
          type="button"
          className="btn secondary block"
          style={{ marginTop: "0.75rem" }}
          onClick={() => void logout()}
        >
          Sign out
        </button>
      </form>
    </div>
  );
}
