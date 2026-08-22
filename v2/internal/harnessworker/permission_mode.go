package harnessworker

import "github.com/agentserver/agentserver/v2/internal/runmanifest"

// Re-export the run-manifest permission vocabulary at the worker boundary so
// callers constructing an AppServerRunRequest cannot accidentally invent a
// second, stringly-typed mode set.
type CodexPermissionMode = runmanifest.CodexPermissionMode

const (
	CodexPermissionModeReadOnly   = runmanifest.CodexPermissionModeReadOnly
	CodexPermissionModeAuto       = runmanifest.CodexPermissionModeAuto
	CodexPermissionModeFullAccess = runmanifest.CodexPermissionModeFullAccess
	DefaultCodexPermissionMode    = runmanifest.DefaultCodexPermissionMode
	CodexPermissionReadOnly       = runmanifest.CodexPermissionReadOnly
	CodexPermissionAuto           = runmanifest.CodexPermissionAuto
	CodexPermissionFullAccess     = runmanifest.CodexPermissionFullAccess
)
