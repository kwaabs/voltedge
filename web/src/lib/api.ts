/**
 * Base path for the Go API. In dev, Vite proxies /api -> localhost:3704
 * (see vite.config.ts) so the browser never needs CORS configured on the
 * API itself. In production this is served from the same origin behind
 * a reverse proxy, same /api prefix.
 */
import { useEditModeStore } from "@/stores/edit-mode-store"

const API_BASE = "/api"

/**
 * The readable companion to the httpOnly session cookie. The server sets
 * both on login (see api/internal/auth/middleware.go setSessionCookies);
 * this one is deliberately NOT httpOnly so the SPA can echo it in the
 * X-CSRF-Token header on every mutating request -- the double-submit half
 * of the CSRF defense. Knowing it is useless without also holding the
 * session cookie, which JS can never read.
 */
const CSRF_COOKIE = "ve_csrf"

/**
 * HTTP methods the server treats as safe -- they never carry a CSRF token
 * and RequireCSRF waves them through. Everything else must echo the token.
 */
const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"])

function readCookie(name: string): string | undefined {
  for (const part of document.cookie.split("; ")) {
    const eq = part.indexOf("=")
    if (eq > -1 && part.slice(0, eq) === name) {
      return decodeURIComponent(part.slice(eq + 1))
    }
  }
  return undefined
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? "GET").toUpperCase()
  const headers: Record<string, string> = { "Content-Type": "application/json", ...(init?.headers as Record<string, string>) }

  // Double-submit CSRF: echo the readable ve_csrf cookie on any mutating
  // request. Safe methods send nothing (and the server expects nothing).
  if (!SAFE_METHODS.has(method)) {
    const csrf = readCookie(CSRF_COOKIE)
    if (csrf) headers["X-CSRF-Token"] = csrf
    // Edit-session tagging: while the user is in edit mode, carry the session id
    // so the server stamps feature_history.edit_session_id and the edit-sessions
    // report can group this batch of changes. Absent outside edit mode.
    const editSession = useEditModeStore.getState().sessionId
    if (editSession) headers["X-Edit-Session"] = editSession
  }

  const res = await fetch(`${API_BASE}${path}`, {
    // Same-origin cookies (session + CSRF) must ride along; explicit so it
    // never depends on the fetch default drifting.
    credentials: "same-origin",
    ...init,
    headers,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new ApiError(res.status, body.error ?? res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

/**
 * The binary sibling of apiFetch: POSTs a JSON body to an endpoint that streams
 * a file rather than JSON (the geographic export, docs/phase-14-export.md). It
 * replays the same same-origin cookies + double-submit CSRF header apiFetch
 * uses (a raw fetch would 403 without it), then -- on success -- returns the
 * response as a Blob together with the filename the server set in
 * Content-Disposition, so the caller saves it under the right name. A failing
 * response is still JSON (`{error}`, sent before any file bytes), so it's
 * decoded into an ApiError exactly like apiFetch.
 */
export async function postForBlob(path: string, body: unknown): Promise<{ blob: Blob; filename: string }> {
  const headers: Record<string, string> = { "Content-Type": "application/json" }
  const csrf = readCookie(CSRF_COOKIE)
  if (csrf) headers["X-CSRF-Token"] = csrf

  const res = await fetch(`${API_BASE}${path}`, {
    method: "POST",
    credentials: "same-origin",
    headers,
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new ApiError(res.status, err.error ?? res.statusText)
  }
  return { blob: await res.blob(), filename: filenameFromDisposition(res.headers.get("Content-Disposition")) }
}

/** Pulls the filename out of a `Content-Disposition: attachment; filename="..."`
 *  header, empty string if absent so the caller can fall back to its own name. */
function filenameFromDisposition(header: string | null): string {
  if (!header) return ""
  const match = /filename="?([^"]+)"?/.exec(header)
  return match ? match[1] : ""
}
