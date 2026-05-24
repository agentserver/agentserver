import { useState } from 'react'
import { Activity, MessageSquare } from 'lucide-react'
import clsx from 'clsx'

type Tab = 'calls' | 'sessions'

interface Props {
  workspaceId: string
}

export default function ExecAuditPanel({ workspaceId }: Props) {
  const [tab, setTab] = useState<Tab>('calls')
  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 border-b border-zinc-200 dark:border-zinc-800 px-4 py-3">
        <TabButton active={tab === 'calls'} onClick={() => setTab('calls')} icon={<MessageSquare size={16} />}>
          Calls
        </TabButton>
        <TabButton active={tab === 'sessions'} onClick={() => setTab('sessions')} icon={<Activity size={16} />}>
          Sessions
        </TabButton>
      </div>
      <div className="flex-1 overflow-auto p-4">
        {tab === 'calls' && <CallsTabPlaceholder workspaceId={workspaceId} />}
        {tab === 'sessions' && <SessionsTabPlaceholder workspaceId={workspaceId} />}
      </div>
    </div>
  )
}

function TabButton({
  active,
  onClick,
  icon,
  children,
}: {
  active: boolean
  onClick: () => void
  icon: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={clsx(
        'flex items-center gap-1.5 px-3 py-1.5 rounded text-sm',
        active
          ? 'bg-zinc-200 dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100'
          : 'text-zinc-600 dark:text-zinc-400 hover:bg-zinc-100 dark:hover:bg-zinc-900',
      )}
    >
      {icon}
      {children}
    </button>
  )
}

// Placeholders — replaced by real CallsTab (T3) + SessionsTab (T5).
function CallsTabPlaceholder({ workspaceId }: { workspaceId: string }) {
  return <div className="text-zinc-500 text-sm">Calls tab (workspace {workspaceId})</div>
}
function SessionsTabPlaceholder({ workspaceId }: { workspaceId: string }) {
  return <div className="text-zinc-500 text-sm">Sessions tab (workspace {workspaceId})</div>
}
