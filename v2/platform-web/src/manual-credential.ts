import type { ResourceAPI } from "@agentserver/v2-web-shared"

type CredentialInput = Parameters<ResourceAPI["createCredential"]>[2]

export function gitCredentialInput(form: FormData, id: string): CredentialInput {
  const displayName = String(form.get("displayName") ?? "").trim()
  const username = String(form.get("username") ?? "")
  const token = String(form.get("token") ?? "")
  if (!displayName || displayName.length > 256 || !username || username.length > 256 || username.trim() !== username || /[:\x00-\x1f\x7f]/u.test(username) || !token || token.length > 8192 || /[\s\x00-\x1f\x7f]/u.test(token)) {
    throw new Error("invalid Git credential fields")
  }
  return { id, displayName, ownerScope: "workspace", authType: "https-token", secret: { host: "code.byted.org", username, token }, makeDefault: form.get("makeDefault") === "on" }
}

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
