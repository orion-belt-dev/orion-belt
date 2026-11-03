import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { Navigate, useSearchParams } from "react-router-dom";
import { useAuth, useHomePath } from "../auth/AuthContext";
import { api } from "../lib/api";
import { b64urlToBuf, bufToB64url } from "../lib/format";
import type { User } from "../lib/types";
import { useToast } from "../components/Toast";
import { ThemeToggle, useTheme } from "../components/ThemeToggle";

type LoginResp = {
  session_token: string;
  access_token?: string;
  user: User;
};

type Method = "device" | "webauthn" | "password";

function preparePublicKeyOptions(publicKey: Record<string, unknown>) {
  const pk = { ...publicKey } as Record<string, unknown>;
  if (typeof pk.challenge === "string") pk.challenge = b64urlToBuf(pk.challenge);
  if (pk.user && typeof (pk.user as { id?: unknown }).id === "string") {
    const u = pk.user as Record<string, unknown>;
    pk.user = { ...u, id: b64urlToBuf(String(u.id)) };
  }
  if (Array.isArray(pk.allowCredentials)) {
    pk.allowCredentials = (pk.allowCredentials as Array<Record<string, unknown>>).map((c) => ({
      ...c,
      id: typeof c.id === "string" ? b64urlToBuf(c.id) : c.id,
    }));
  }
  return pk as unknown as PublicKeyCredentialRequestOptions;
}

export function LoginPage() {
  const { user, version, login, ready } = useAuth();
  const home = useHomePath();
  const { toast } = useToast();
  const { theme, toggle } = useTheme();
  const [searchParams] = useSearchParams();
  const [method, setMethod] = useState<Method>(searchParams.get("code") ? "device" : "password");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [totp, setTotp] = useState("");
  const [ticket, setTicket] = useState("");
  const [needTotp, setNeedTotp] = useState(false);
  const totpRef = useRef<HTMLInputElement>(null);
  // The code field appears after the password step is submitted; move focus
  // to it so keyboard and screen-reader users land on the new field.
  useEffect(() => {
    if (needTotp) totpRef.current?.focus();
  }, [needTotp]);
  const [code, setCode] = useState(searchParams.get("code") || "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const fromURL = searchParams.get("code");
    if (fromURL) void redeemCode(fromURL);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (ready && user) return <Navigate to={home} replace />;

  async function redeemCode(value: string) {
    if (!value.trim()) return;
    setError("");
    setBusy(true);
    try {
      const data = await api<LoginResp>("/public/auth/browser-bootstrap/redeem", {
        method: "POST",
        body: JSON.stringify({ code: value.trim() }),
      });
      login(data.session_token, data.user, data.access_token || "");
      toast("Signed in");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function onSubmitCode(e: FormEvent) {
    e.preventDefault();
    await redeemCode(code);
  }

  async function onWebAuthn() {
    setError("");
    setBusy(true);
    try {
      const begin = await api<{ publicKey: Record<string, unknown> }>("/public/webauthn/login/begin", {
        method: "POST",
        body: JSON.stringify({ username: username.trim() }),
      });
      const cred = (await navigator.credentials.get({
        publicKey: preparePublicKeyOptions(begin.publicKey),
      })) as PublicKeyCredential | null;
      if (!cred) throw new Error("WebAuthn cancelled");
      const r = cred.response as AuthenticatorAssertionResponse;
      const payload = {
        username: username.trim(),
        response: {
          id: cred.id,
          rawId: bufToB64url(cred.rawId),
          type: cred.type,
          response: {
            clientDataJSON: bufToB64url(r.clientDataJSON),
            authenticatorData: bufToB64url(r.authenticatorData),
            signature: bufToB64url(r.signature),
            userHandle: r.userHandle ? bufToB64url(r.userHandle) : undefined,
          },
        },
      };
      const data = await api<LoginResp>("/public/webauthn/login/finish", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      login(data.session_token, data.user, data.access_token || "");
      toast("Signed in with WebAuthn");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function onPassword(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const body: Record<string, string> = {
        username: username.trim(),
        password,
      };
      if (needTotp && ticket) {
        body.ticket = ticket;
        body.totp_code = totp.trim();
      }
      const data = await api<LoginResp & { need_totp?: boolean; ticket?: string }>("/public/login/password", {
        method: "POST",
        body: JSON.stringify(body),
      });
      if (data.need_totp && data.ticket) {
        setTicket(data.ticket);
        setNeedTotp(true);
        toast("Enter your authenticator code");
        return;
      }
      if (!data.session_token || !data.user) {
        throw new Error("Unexpected login response");
      }
      login(data.session_token, data.user, data.access_token || "");
      toast("Signed in");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const ver = version?.display || version?.version || "…";

  return (
    <div className="login-stage">
      <div className="login-theme-toggle">
        <ThemeToggle theme={theme} onToggle={toggle} />
      </div>
      <div className="card login-panel">
        <h1 className="login-brand">
          Orion <em>Belt</em>
        </h1>
        <p className="login-tag">Sign in to your gateway</p>
        <div className="muted mono" style={{ marginBottom: "1rem", fontSize: "0.75rem" }}>
          {ver}
        </div>

        <div className="row" style={{ marginBottom: "1rem", flexWrap: "wrap", gap: "0.4rem" }}>
          {(
            [
              ["password", "Password"],
              ["webauthn", "Security key"],
              ["device", "Device code"],
            ] as const
          ).map(([id, label]) => (
            <button
              key={id}
              type="button"
              className={`btn sm${method === id ? "" : " secondary"}`}
              onClick={() => {
                setMethod(id);
                setError("");
                setNeedTotp(false);
                setTicket("");
                setTotp("");
              }}
            >
              {label}
            </button>
          ))}
        </div>

        {method !== "device" ? (
          <>
            <label className="field" htmlFor="login-username">Username</label>
            <input id="login-username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
          </>
        ) : null}

        {method === "webauthn" ? (
          <button className="btn block" type="button" style={{ marginTop: "0.75rem" }} disabled={busy} onClick={() => void onWebAuthn()}>
            Sign in with security key
          </button>
        ) : null}

        {method === "password" ? (
          <form onSubmit={(e) => void onPassword(e)}>
            <label className="field" htmlFor="login-password">Password</label>
            <input
              id="login-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              disabled={needTotp}
            />
            {needTotp ? (
              <>
                <label className="field" htmlFor="login-authenticator-code">Authenticator code</label>
                <input
                  id="login-authenticator-code"
                  value={totp}
                  onChange={(e) => setTotp(e.target.value)}
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  required
                  ref={totpRef}
                />
              </>
            ) : null}
            <button className="btn block" type="submit" disabled={busy} style={{ marginTop: "0.85rem" }}>
              {needTotp ? "Verify and sign in" : "Continue"}
            </button>
          </form>
        ) : null}

        {method === "device" ? (
          <form onSubmit={(e) => void onSubmitCode(e)}>
            <p className="muted" style={{ margin: "0 0 0.75rem", fontSize: "0.85rem" }}>
              On your laptop run <code>osh login</code>, then paste the code (or open the printed URL).
            </p>
            <label className="field" htmlFor="login-sign-in-code">Sign-in code</label>
            <input
              id="login-sign-in-code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="e.g. 7K4M9PQRXZ"
              autoComplete="off"
              className="mono"
            />
            <button className="btn block" type="submit" disabled={busy || !code.trim()} style={{ marginTop: "0.85rem" }}>
              Redeem code
            </button>
          </form>
        ) : null}

        {error ? <div className="err">{error}</div> : null}
      </div>
    </div>
  );
}
