import { ApiError } from "./types";

const TOKEN_KEY = "ob_session";
const JWT_KEY = "ob_jwt";
const USER_KEY = "ob_user";

export function getSessionToken(): string {
  return localStorage.getItem(TOKEN_KEY) || "";
}

export function getJwt(): string {
  return localStorage.getItem(JWT_KEY) || "";
}

export function loadStoredUser<T>(): T | null {
  try {
    return JSON.parse(localStorage.getItem(USER_KEY) || "null") as T | null;
  } catch {
    return null;
  }
}

export function persistAuth(session: string, jwt: string, user: unknown) {
  if (session) localStorage.setItem(TOKEN_KEY, session);
  else localStorage.removeItem(TOKEN_KEY);
  if (jwt) localStorage.setItem(JWT_KEY, jwt);
  else localStorage.removeItem(JWT_KEY);
  if (user) localStorage.setItem(USER_KEY, JSON.stringify(user));
  else localStorage.removeItem(USER_KEY);
  if (session) {
    document.cookie = `session_token=${encodeURIComponent(session)}; path=/; SameSite=Lax`;
  } else {
    document.cookie = "session_token=; path=/; Max-Age=0; SameSite=Lax";
  }
}

export function clearAuth() {
  persistAuth("", "", null);
}

export async function api<T = unknown>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers = new Headers(opts.headers || {});
  if (!(opts.body instanceof FormData) && !headers.has("Content-Type") && opts.body) {
    headers.set("Content-Type", "application/json");
  }
  const token = getSessionToken();
  const jwt = getJwt();
  if (token) headers.set("X-Session-Token", token);
  if (jwt && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${jwt}`);

  const res = await fetch(`/api/v1${path}`, { ...opts, headers });
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!res.ok) {
    const msg =
      (data && typeof data === "object" && ("error" in data || "message" in data)
        ? String((data as { error?: string; message?: string }).error || (data as { message?: string }).message)
        : null) ||
      res.statusText ||
      "request failed";
    throw new ApiError(msg, res.status, data);
  }
  return data as T;
}

export async function apiRaw(path: string, opts: RequestInit = {}): Promise<Response> {
  const headers = new Headers(opts.headers || {});
  const token = getSessionToken();
  const jwt = getJwt();
  if (token) headers.set("X-Session-Token", token);
  if (jwt && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${jwt}`);
  return fetch(`/api/v1${path}`, { ...opts, headers });
}

function parseContentDispositionFilename(header: string | null): string | null {
  if (!header) return null;
  const match = /filename\*=UTF-8''([^;]+)|filename="?([^";]+)"?/i.exec(header);
  const raw = match?.[1] || match?.[2];
  if (!raw) return null;
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

export async function apiDownload(path: string, fallbackFilename: string): Promise<void> {
  const res = await apiRaw(path);
  if (!res.ok) {
    const text = await res.text();
    let message = text || "download failed";
    try {
      const payload = JSON.parse(text) as { error?: string; message?: string };
      message = payload.error || payload.message || message;
    } catch {
      // Ignore parse errors and use raw response text.
    }
    throw new ApiError(message, res.status, text);
  }

  const blob = await res.blob();
  const fileName = parseContentDispositionFilename(res.headers.get("Content-Disposition")) || fallbackFilename;
  const href = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = href;
  a.download = fileName;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(href);
}

// apiPage fetches a paged list and the total from X-Total-Count.
export async function apiPage<T>(path: string): Promise<{ rows: T[]; total: number }> {
  const headers = new Headers();
  const token = getSessionToken();
  const jwt = getJwt();
  if (token) headers.set("X-Session-Token", token);
  if (jwt) headers.set("Authorization", `Bearer ${jwt}`);
  const res = await fetch(`/api/v1${path}`, { headers });
  const data = (await res.json().catch(() => null)) as unknown;
  if (!res.ok) {
    const msg = data && typeof data === "object" && "error" in data ? String((data as { error: string }).error) : res.statusText;
    throw new ApiError(msg || "request failed", res.status, data);
  }
  const rows = (Array.isArray(data) ? data : []) as T[];
  const total = Number(res.headers.get("X-Total-Count") ?? rows.length);
  return { rows, total: Number.isFinite(total) ? total : rows.length };
}
