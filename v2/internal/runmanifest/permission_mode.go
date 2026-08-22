package runmanifest

import "fmt"

// CodexPermissionMode is one of Codex's built-in permission preset IDs (the
// IDs in codex-rs/utils/approval-presets). The value is deployment/run
// authority, not model input: it is carried by the signed run manifest and
// translated to the native app-server fields by the worker.
//
// The empty value is accepted only for backwards compatibility with manifests
// produced before permissionMode was added.  It normalizes to the safe
// read-only default for configuration purposes and is never emitted by the
// launch preparer for new runs.  The worker retains the older, stricter
// approval-never projection when it executes an actually omitted manifest
// field.
type CodexPermissionMode string

const (
	CodexPermissionModeReadOnly   CodexPermissionMode = "read-only"
	CodexPermissionModeAuto       CodexPermissionMode = "auto"
	CodexPermissionModeFullAccess CodexPermissionMode = "full-access"
	DefaultCodexPermissionMode                        = CodexPermissionModeReadOnly

	// Short aliases keep call sites readable while retaining the explicit
	// Mode-prefixed names above.
	CodexPermissionReadOnly   = CodexPermissionModeReadOnly
	CodexPermissionAuto       = CodexPermissionModeAuto
	CodexPermissionFullAccess = CodexPermissionModeFullAccess
)

// codexPermissionModes is the closed set accepted by the deployment profile
// and run-manifest validators.  Keep this ordered to match the documented
// user-facing choices and the safe default first.  It is intentionally not
// exported as a mutable slice: changing the accepted set would be a security
// policy change.
var codexPermissionModes = [...]CodexPermissionMode{
	CodexPermissionModeReadOnly,
	CodexPermissionModeAuto,
	CodexPermissionModeFullAccess,
}

// Effective returns the canonical mode, applying the backwards-compatible
// safe default to an omitted field.
func (mode CodexPermissionMode) Effective() (CodexPermissionMode, error) {
	if mode == "" {
		return DefaultCodexPermissionMode, nil
	}
	for _, supported := range codexPermissionModes {
		if mode == supported {
			return mode, nil
		}
	}
	return "", fmt.Errorf("codex permission mode %q is unsupported", mode)
}

// SupportedCodexPermissionModes returns a defensive copy of the accepted
// profile values for configuration UIs and diagnostics.
func SupportedCodexPermissionModes() []CodexPermissionMode {
	return append([]CodexPermissionMode(nil), codexPermissionModes[:]...)
}

// Validate checks a manifest/profile value.  Empty is deliberately allowed
// here for old signed manifests; callers that need an explicit deployment
// value should use Effective and persist the returned canonical mode.
func (mode CodexPermissionMode) Validate() error {
	_, err := mode.Effective()
	return err
}
