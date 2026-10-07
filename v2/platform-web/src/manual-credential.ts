import type { ResourceAPI } from "@agentserver/v2-web-shared"

type CredentialInput = Parameters<ResourceAPI["createCredential"]>[2]

// Credentials exist only in the HTTPS request body, never persisted UI state.
export function byteCloudCredentialInput(form: FormData, id: string): CredentialInput {
  const displayName = String(form.get("displayName") ?? "").trim()
  const accessKeyId = String(form.get("accessKeyId") ?? "")
  const secretAccessKey = String(form.get("secretAccessKey") ?? "")
  if (!displayName || displayName.length > 256 || ![accessKeyId, secretAccessKey].every(value => value.length > 0 && value.length <= 4096 && value.trim() === value && !/[\r\n\0]/u.test(value))) {
    throw new Error("invalid AK/SK credential fields")
  }
  return { id, displayName, ownerScope: "workspace", authType: "aksk", secret: { accessKeyId, secretAccessKey }, makeDefault: form.get("makeDefault") === "on" }
}
