import { useState, useEffect, useCallback } from 'react'
import { Loader2 } from 'lucide-react'
import {
  checkAuth,
  listWorkspaces,
  listSandboxes,
  getMe,
  pauseSandbox,
  resumeSandbox,
  deleteSandbox,
  type Workspace,
  type Sandbox,
} from './lib/api'
import { Login } from './components/Login'
import { SandboxList } from './components/SandboxList'
import { SandboxDetail } from './components/SandboxDetail'
import { WorkspaceDetail } from './components/WorkspaceDetail'
import { AdminPanel } from './components/AdminPanel'

export interface UserInfo {
  id: string
  username: string
  email: string
  name?: string | null
  picture?: string | null
  role: string
}

export default function App() {
  const [authed, setAuthed] = useState<boolean | null>(null)
  const [user, setUser] = useState<UserInfo | null>(null)
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<string | null>(null)
  const [sandboxes, setSandboxes] = useState<Sandbox[]>([])
  const [activeSandboxId, setActiveSandboxId] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [showAdmin, setShowAdmin] = useState(false)
  const [showWorkspaceDetail, setShowWorkspaceDetail] = useState(false)

  const refreshSandboxes = useCallback(async () => {
    if (!selectedWorkspaceId) return
    try {
      const list = await listSandboxes(selectedWorkspaceId)
      setSandboxes(list)
    } catch {
      // ignore
    }
  }, [selectedWorkspaceId])

  useEffect(() => {
    checkAuth().then((ok) => {
      setAuthed(ok)
      if (ok) {
        listWorkspaces().then((ws) => {
          setWorkspaces(ws)
          if (ws.length > 0) setSelectedWorkspaceId(ws[0].id)
        }).catch(() => {})
        getMe().then(setUser).catch(() => {})
      }
    })
  }, [])

  useEffect(() => {
    if (selectedWorkspaceId) {
      refreshSandboxes()
      setActiveSandboxId(null)
    } else {
      setSandboxes([])
      setActiveSandboxId(null)
    }
    setShowWorkspaceDetail(false)
  }, [selectedWorkspaceId, refreshSandboxes])

  const handleSelectWorkspace = useCallback((id: string) => {
    setSelectedWorkspaceId(id || null)
  }, [])

  const handleLogout = useCallback(() => {
    setAuthed(false)
    setUser(null)
    setWorkspaces([])
    setSelectedWorkspaceId(null)
    setSandboxes([])
    setActiveSandboxId(null)
  }, [])

  const handlePause = useCallback(async (id: string) => {
    try {
      await pauseSandbox(id)
      setSandboxes((prev) => prev.map((s) => (s.id === id ? { ...s, status: 'pausing' as const } : s)))
    } catch { /* ignore */ }
  }, [])

  const handleResume = useCallback(async (id: string) => {
    try {
      await resumeSandbox(id)
      setSandboxes((prev) => prev.map((s) => (s.id === id ? { ...s, status: 'resuming' as const } : s)))
    } catch { /* ignore */ }
  }, [])

  const handleDelete = useCallback(async (id: string) => {
    try {
      await deleteSandbox(id)
      setSandboxes((prev) => prev.filter((s) => s.id !== id))
      if (activeSandboxId === id) setActiveSandboxId(null)
    } catch { /* ignore */ }
  }, [activeSandboxId])

  const handleSelectSandbox = useCallback((id: string) => {
    setActiveSandboxId(id)
    setShowWorkspaceDetail(false)
    setShowAdmin(false)
  }, [])

  const handleShowWorkspaceDetail = useCallback(() => {
    setShowWorkspaceDetail(true)
    setActiveSandboxId(null)
    setShowAdmin(false)
  }, [])

  const handleShowAdmin = useCallback(() => {
    setShowAdmin(true)
    setActiveSandboxId(null)
    setShowWorkspaceDetail(false)
  }, [])

  if (authed === null) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <span className="text-[var(--muted-foreground)]">Loading...</span>
      </div>
    )
  }

  if (!authed) {
    return (
      <Login
        onSuccess={() => {
          setAuthed(true)
          listWorkspaces().then((ws) => {
            setWorkspaces(ws)
            if (ws.length > 0) setSelectedWorkspaceId(ws[0].id)
          }).catch(() => {})
          getMe().then(setUser).catch(() => {})
        }}
      />
    )
  }

  const activeSandboxData = sandboxes.find((s) => s.id === activeSandboxId)
  const selectedWorkspace = workspaces.find((w) => w.id === selectedWorkspaceId)

  let mainContent
  if (showAdmin) {
    mainContent = <AdminPanel onBack={() => setShowAdmin(false)} />
  } else if (creating) {
    mainContent = (
      <div className="flex flex-col items-center justify-center gap-3 h-full">
        <Loader2 size={24} className="animate-spin text-[var(--muted-foreground)]" />
        <span className="text-[var(--muted-foreground)]">Creating sandbox...</span>
      </div>
    )
  } else if (showWorkspaceDetail && selectedWorkspace) {
    mainContent = <WorkspaceDetail workspace={selectedWorkspace} />
  } else if (activeSandboxId && activeSandboxData) {
    mainContent = (
      <SandboxDetail
        sandbox={activeSandboxData}
        onPause={handlePause}
        onResume={handleResume}
        onDelete={handleDelete}
      />
    )
  } else {
    mainContent = (
      <div className="flex items-center justify-center h-full">
        <span className="text-[var(--muted-foreground)]">Select or create a sandbox</span>
      </div>
    )
  }

  return (
    <div className="flex h-screen">
      <SandboxList
        workspaces={workspaces}
        setWorkspaces={setWorkspaces}
        selectedWorkspaceId={selectedWorkspaceId}
        onSelectWorkspace={handleSelectWorkspace}
        sandboxes={sandboxes}
        setSandboxes={setSandboxes}
        activeSandboxId={activeSandboxId}
        onSelectSandbox={handleSelectSandbox}
        onRefreshSandboxes={refreshSandboxes}
        creating={creating}
        setCreating={setCreating}
        user={user}
        onLogout={handleLogout}
        onShowAdmin={user?.role === 'admin' ? handleShowAdmin : undefined}
        onShowWorkspaceDetail={handleShowWorkspaceDetail}
      />
      <div className="flex flex-1 flex-col bg-[var(--background)]">
        {mainContent}
      </div>
    </div>
  )
}
