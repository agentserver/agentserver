import type { CredentialAuthorization } from "@agentserver/v2-web-shared"

const referencePrefix = "agentserver.v2.credential-authorization."
const identifier = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u
const providerKind = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/u

export interface CredentialAuthorizationReference {
  workspaceId: string
  kind: string
  authorizationId: string
}

export function credentialAuthorizationStorageKey(workspaceId: string): string {
  if (!identifier.test(workspaceId)) throw new Error("The credential authorization workspace is invalid.")
  return referencePrefix + workspaceId
}

export function persistCredentialAuthorization(storage: Storage, reference: CredentialAuthorizationReference): void {
  validateReference(reference)
  storage.setItem(credentialAuthorizationStorageKey(reference.workspaceId), JSON.stringify(reference))
}

export function restoreCredentialAuthorization(storage: Storage, workspaceId: string): CredentialAuthorizationReference | null {
  const key = credentialAuthorizationStorageKey(workspaceId)
  const raw = storage.getItem(key)
  if (!raw || raw.length > 2048) {
    storage.removeItem(key)
    return null
  }
  try {
    const value = JSON.parse(raw) as CredentialAuthorizationReference
    if (!value || typeof value !== "object" || Array.isArray(value) ||
        Object.keys(value).sort().join(",") !== "authorizationId,kind,workspaceId") throw new Error("invalid reference")
    validateReference(value)
    if (value.workspaceId !== workspaceId) throw new Error("workspace mismatch")
    return value
  } catch {
    storage.removeItem(key)
    return null
  }
}

export function clearCredentialAuthorization(storage: Storage, workspaceId: string): void {
  storage.removeItem(credentialAuthorizationStorageKey(workspaceId))
}

export function credentialAuthorizationTerminal(status: CredentialAuthorization["status"]): boolean {
  return status !== "pending"
}

export function credentialAuthorizationPollDelay(authorization: CredentialAuthorization, now = Date.now()): number {
  if (credentialAuthorizationTerminal(authorization.status)) return -1
  const next = Date.parse(authorization.nextPollAt)
  const expiry = Date.parse(authorization.expiresAt)
  if (!Number.isFinite(next) || !Number.isFinite(expiry) || expiry <= now) return 0
  return Math.max(0, Math.min(next - now, expiry - now, 60_000))
}

function validateReference(reference: CredentialAuthorizationReference): void {
  if (!identifier.test(reference.workspaceId) || !identifier.test(reference.authorizationId) || !providerKind.test(reference.kind)) {
    throw new Error("The credential authorization reference is invalid.")
  }
}
