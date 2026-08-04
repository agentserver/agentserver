import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react"
import { EdgeAPI, type AuthorizationConfig } from "./api"
import {
  authorizationSessionStorageKey,
  beginAuthorization,
  completeAuthorization,
  persistAuthorizationSession,
  readAuthorizationCallback,
  removeAuthorizationSession,
  restoreAuthorizationSession,
  validateAuthorizationConfig,
  type AuthorizationSession,
  type AuthMode,
} from "./oauth"
import { safeError } from "./utils"

interface AuthState {
  status: "loading" | "signed-out" | "signed-in" | "error"
  config: AuthorizationConfig | null
  token: string
  scopes: readonly string[]
  workspaceId: string
  expiresAt: number
  error: string
}

interface AuthContextValue extends AuthState {
  signIn: (workspaceId?: string, returnPath?: string) => Promise<void>
  signOut: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ mode, children }: { mode: AuthMode; children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: "loading", config: null, token: "", scopes: [], workspaceId: "", expiresAt: 0, error: "" })

  useEffect(() => {
    let active = true
    void (async () => {
      try {
        const config = validateAuthorizationConfig(await new EdgeAPI(window.location.origin).authorizationConfig(), mode)
        const callback = readAuthorizationCallback(window.location.search)
        if (!callback) {
          let session: AuthorizationSession | null = null
          try { session = restoreAuthorizationSession(window.localStorage, config, mode) } catch { /* localStorage may be unavailable */ }
          if (active) setState(session ? signedInState(config, session) : signedOutState(config))
          return
        }
        const result = await completeAuthorization(config, mode, callback)
        history.replaceState(null, "", result.returnPath)
        window.dispatchEvent(new PopStateEvent("popstate"))
        persistAuthorizationSession(window.localStorage, config, mode, result)
        if (active) setState(signedInState(config, result))
      } catch (error) {
        history.replaceState(null, "", window.location.pathname)
        if (active) setState((current) => ({ ...current, status: "error", token: "", scopes: [], workspaceId: "", expiresAt: 0, error: safeError(error) }))
      }
    })()
    return () => { active = false }
  }, [mode])

  useEffect(() => {
    if (!state.config) return
    const config = state.config
    const key = authorizationSessionStorageKey(mode)
    const synchronize = (event: StorageEvent) => {
      if (event.key !== null && event.key !== key) return
      if (event.storageArea && event.storageArea !== window.localStorage) return
      try {
        const session = restoreAuthorizationSession(window.localStorage, config, mode)
        setState(session ? signedInState(config, session) : signedOutState(config))
      } catch {
        setState(signedOutState(config))
      }
    }
    window.addEventListener("storage", synchronize)
    return () => window.removeEventListener("storage", synchronize)
  }, [mode, state.config])

  useEffect(() => {
    if (state.status !== "signed-in" || !state.config) return
    const config = state.config
    const remaining = state.expiresAt - Date.now()
    const expire = () => {
      try {
        const stored = restoreAuthorizationSession(window.localStorage, config, mode)
        if (stored && (stored.token !== state.token || stored.expiresAt !== state.expiresAt)) return
        removeAuthorizationSession(window.localStorage, mode)
      } catch { /* in-memory expiry still applies */ }
      setState((current) => current.token === state.token && current.expiresAt === state.expiresAt ? signedOutState(config) : current)
    }
    if (remaining <= 0) { expire(); return }
    const timer = window.setTimeout(expire, remaining)
    return () => window.clearTimeout(timer)
  }, [mode, state.config, state.expiresAt, state.status, state.token])

  const signIn = useCallback(async (workspaceId = "", returnPath = window.location.pathname) => {
    if (!state.config) throw new Error("Authorization is not ready.")
    await beginAuthorization(state.config, mode, workspaceId, returnPath)
  }, [mode, state.config])

  const signOut = useCallback(() => {
    try { removeAuthorizationSession(window.localStorage, mode) } catch { /* local state must still be cleared */ }
    setState((current) => current.config ? signedOutState(current.config) : { ...current, status: "signed-out", token: "", scopes: [], workspaceId: "", expiresAt: 0, error: "" })
  }, [mode])

  const value = useMemo<AuthContextValue>(() => ({ ...state, signIn, signOut }), [state, signIn, signOut])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error("useAuth must be used inside AuthProvider.")
  return value
}

function signedInState(config: AuthorizationConfig, session: AuthorizationSession): AuthState {
  return { status: "signed-in", config, token: session.token, scopes: session.scopes, workspaceId: session.workspaceId, expiresAt: session.expiresAt, error: "" }
}

function signedOutState(config: AuthorizationConfig): AuthState {
  return { status: "signed-out", config, token: "", scopes: [], workspaceId: "", expiresAt: 0, error: "" }
}
