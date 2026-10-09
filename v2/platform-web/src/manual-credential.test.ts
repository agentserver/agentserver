import { describe, expect, it } from "vitest"
import { byteCloudCredentialInput, gitCredentialInput } from "./manual-credential"

function fields() {
  const form = new FormData()
  form.set("displayName", "Managed bkectl")
  form.set("accessKeyId", "test-ak")
  form.set("secretAccessKey", "test-sk")
  form.set("makeDefault", "on")
  return form
}

describe("ByteCloud manual credential upload", () => {
  it("creates a distinct AK/SK binding without modifying the existing OAuth binding", () => {
    expect(byteCloudCredentialInput(fields(), "new-binding-id")).toEqual({
      id: "new-binding-id", displayName: "Managed bkectl", ownerScope: "workspace", authType: "aksk",
      secret: { accessKeyId: "test-ak", secretAccessKey: "test-sk" }, makeDefault: true,
    })
  })
  it("does not change the default without the owner's checked choice", () => {
    const form = fields(); form.delete("makeDefault")
    expect(byteCloudCredentialInput(form, "new-binding-id").makeDefault).toBe(false)
  })
  it.each(["", " test-sk", "test-sk\n", "a".repeat(4097)])("rejects invalid keys without including their value in errors", (value) => {
    const form = fields(); form.set("secretAccessKey", value)
    expect(() => byteCloudCredentialInput(form, "new-binding-id")).toThrow("invalid AK/SK credential fields")
  })
})

describe("Git manual credential upload", () => {
  function gitFields() {
    const form = new FormData()
    form.set("displayName", "Codebase")
    form.set("username", "test-user")
    form.set("token", "private-test-token")
    return form
  }
  it("uses a workspace-scoped host-bound HTTPS credential", () => {
    expect(gitCredentialInput(gitFields(), "binding-id")).toEqual({
      id: "binding-id", displayName: "Codebase", ownerScope: "workspace", authType: "https-token",
      secret: { host: "code.byted.org", username: "test-user", token: "private-test-token" }, makeDefault: false,
    })
  })
  it.each(["", " token", "token\n", "token\t", "a".repeat(8193)])("rejects invalid tokens without echoing them", token => {
    const form = gitFields(); form.set("token", token)
    expect(() => gitCredentialInput(form, "binding-id")).toThrow("invalid Git credential fields")
  })
  it("rejects a username that changes HTTP basic authentication semantics", () => {
    const form = gitFields(); form.set("username", "user:secret")
    expect(() => gitCredentialInput(form, "binding-id")).toThrow("invalid Git credential fields")
  })
})
