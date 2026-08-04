import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

const canonicalUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function canonicalID(label: string, value: string): string {
  if (!canonicalUUID.test(value) || value === "00000000-0000-0000-0000-000000000000") {
    throw new Error(`${label} must be a non-zero canonical UUID.`)
  }
  return value
}

export function newID(): string {
  if (!globalThis.crypto?.randomUUID) throw new Error("Secure UUID generation is unavailable.")
  return canonicalID("generated ID", globalThis.crypto.randomUUID())
}

export function randomSecret(prefix = "web"): string {
  if (!globalThis.crypto?.getRandomValues) throw new Error("Secure random generation is unavailable.")
  const bytes = new Uint8Array(24)
  globalThis.crypto.getRandomValues(bytes)
  let binary = ""
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return `${prefix}-${btoa(binary).replace(/\+/gu, "-").replace(/\//gu, "_").replace(/=+$/gu, "")}`
}

export function boundedText(label: string, value: string, maximumBytes: number): string {
  const normalized = value.trim()
  if (!normalized || normalized.includes("\0") || new TextEncoder().encode(normalized).length > maximumBytes) {
    throw new Error(`${label} is empty or outside protocol bounds.`)
  }
  return normalized
}

export function safeError(error: unknown): string {
  if (error instanceof Error && error.message) return error.message.slice(0, 4096)
  return "The request could not be completed."
}

export function shortID(value: string): string {
  return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value
}

export function formatDate(value: string, locale: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? "—" : new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date)
}

export function titleFromPrompt(prompt: string): string {
  const normalized = prompt.replace(/\s+/gu, " ").trim() || "New conversation"
  let result = ""
  for (const character of normalized) {
    if (new TextEncoder().encode(result + character).length > 120) break
    result += character
  }
  return result || "New conversation"
}
