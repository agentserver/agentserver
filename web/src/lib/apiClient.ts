// apiClient is the single fetch wrapper used by the generated-typed
// helpers in api.ts. Responsibilities:
//   - include cookie credentials so the session cookie travels with
//     every request
//   - serialize JSON bodies + set Content-Type when a body is given
//   - on non-2xx, throw ApiError carrying status + parsed body so
//     callers can branch on { status: 401 } etc.

export class ApiError extends Error {
  readonly status: number
  readonly body: unknown

  constructor(status: number, body: unknown, message?: string) {
    super(message ?? `HTTP ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

export interface ApiRequest {
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  path: string
  body?: unknown
}

export async function apiFetch<TResponse>(req: ApiRequest): Promise<TResponse> {
  const init: RequestInit = {
    method: req.method,
    credentials: 'include',
    headers: req.body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: req.body !== undefined ? JSON.stringify(req.body) : undefined,
  }
  const res = await fetch(req.path, init)
  if (!res.ok) {
    let parsed: unknown
    const text = await res.text()
    try { parsed = JSON.parse(text) } catch { parsed = text }
    throw new ApiError(res.status, parsed)
  }
  // 204 / empty body → return undefined as TResponse
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as TResponse
}
