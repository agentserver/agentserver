const TRANSITIONAL_SANDBOX_STATUSES = new Set(['creating', 'pausing', 'resuming'])
const LOCAL_AGENT_LIVE_STATUSES = new Set(['running', 'offline'])

type SandboxStatusPollItem = {
  status: string
  is_local: boolean
}

export function shouldPollSandboxStatuses(sandboxes: SandboxStatusPollItem[]): boolean {
  return sandboxes.some((sandbox) => {
    if (TRANSITIONAL_SANDBOX_STATUSES.has(sandbox.status)) return true
    return sandbox.is_local && LOCAL_AGENT_LIVE_STATUSES.has(sandbox.status)
  })
}

export function sandboxStatusPollIntervalMs(sandboxes: SandboxStatusPollItem[]): number | null {
  if (sandboxes.some((sandbox) => TRANSITIONAL_SANDBOX_STATUSES.has(sandbox.status))) {
    return 2000
  }
  if (sandboxes.some((sandbox) => sandbox.is_local && LOCAL_AGENT_LIVE_STATUSES.has(sandbox.status))) {
    return 10000
  }
  return null
}
