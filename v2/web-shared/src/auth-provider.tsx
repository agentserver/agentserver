import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react"
import { EdgeAPI, type AuthorizationConfig } from "./api"
import { beginAuthorization, completeAuthorization, readAuthorizationCallback, validateAuthorizationConfig, type AuthMode } from "./oauth"
import { safeError } from "./utils"

interface AuthState {
  status: "loading" | "signed-out" | "signed-in" | "error"
  config: AuthorizationConfig | null
  token: string
  scopes: readonly string[]
  workspaceId: string
  error: string
}

interface AuthContextValue extends AuthState {
  signIn: (workspaceId?: string, returnPath?: string) => Promise<void>
  signOut: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ mode, children }: { mode: AuthMode; children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: "loading", config: null, token: "", scopes: [], workspaceId: "", error: "" })

  useEffect(() => {
    let active = true
    void (async () => {
      try {
        const config = validateAuthorizationConfig(await new EdgeAPI(window.location.origin).authorizationConfig(), mode)
        const callback = readAuthorizationCallback(window.location.search)
        if (!callback) {
          if (active) setState({ status: "signed-out", config, token: "", scopes: [], workspaceId: "", error: "" })
          return
        }
        const result = await completeAuthorization(config, mode, callback)
        history.replaceState(null, "", result.returnPath)
        window.dispatchEvent(new PopStateEvent("popstate"))
        if (active) setState({ status: "signed-in", config, token: result.token, scopes: result.scopes, workspaceId: result.workspaceId, error: "" })
      } catch (error) {
        history.replaceState(null, "", window.location.pathname)
        if (active) setState((current) => ({ ...current, status: "error", token: "", error: safeError(error) }))
      }
    })()
    return () => { active = false }
  }, [mode])

  const signIn = useCallback(async (workspaceId = "", returnPath = window.location.pathname) => {
    if (!state.config) throw new Error("Authorization is not ready.")
    await beginAuthorization(state.config, mode, workspaceId, returnPath)
  }, [mode, state.config])

  const signOut = useCallback(() => {
    setState((current) => ({ ...current, status: "signed-out", token: "", scopes: [], workspaceId: "", error: "" }))
  }, [])

  const value = useMemo<AuthContextValue>(() => ({ ...state, signIn, signOut }), [state, signIn, signOut])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error("useAuth must be used inside AuthProvider.")
  return value
}
