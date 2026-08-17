// Package bkectlpolicy describes the managed bkectl process-credential
// contract. It deliberately does not authorize bkectl command paths: command
// authorization belongs to bkectl and its downstream IAM/policy engines.
package bkectlpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

const (
	PackID         = "bkectl-managed@v1"
	Executable     = "bkectl"
	CredentialKind = "bytecloud"
	CredentialHost = "cloud-i18n-sg.bytedance.net"
	SourceRevision = "d813842ab03b24f2ac7a5b374507c273f41cbf21"
	CLIVersion     = SourceRevision
	CLISHA256      = "0331eb9836e46034d07bc88c52fa7af738dff7ad0b2b83d85ffe7cd6dfb7a590"
	CLISizeBytes   = int64(34263166)

	SkillPackSHA256 = "b752c88857ca7035580e9a9d51e9c63a09fd1bda28ce290c4e71b351c3779e51"

	// CredentialContractDocument is hashed into the generic policySha256
	// contract field shared by managed credential providers. It records that
	// AgentServer does not maintain a bkectl command allowlist.
	CredentialContractDocument = "agentserver-v2/bkectl-process-credential/v1\n" +
		"command_paths=unrestricted\n" +
		"authorization=downstream\n" +
		"discovery=without-credential\n" +
		"credential_disclosure=auth-get-jwt-denied\n"
)

var (
	// ErrCredentialDisclosureDenied is the sole bkectl-specific command guard.
	// The injected workspace JWT must never be returned to the model as command
	// output. It is not a business-command authorization policy.
	ErrCredentialDisclosureDenied = errors.New("managed bkectl may not export the injected workspace credential")
	policySHA256                  = sha256.Sum256([]byte(CredentialContractDocument))
)

// CredentialRequired reports whether a managed bkectl invocation needs the
// workspace ByteCloud credential. All present and future business command
// paths are accepted. Help/version discovery runs without a credential, and
// the one command that directly prints the injected JWT is rejected.
func CredentialRequired(arguments []string) (bool, error) {
	if safeDiscoveryInvocation(arguments) {
		return false, nil
	}
	if credentialDisclosureInvocation(arguments) {
		return false, ErrCredentialDisclosureDenied
	}
	return true, nil
}

func safeDiscoveryInvocation(arguments []string) bool {
	if len(arguments) == 0 {
		return true
	}
	for _, argument := range arguments {
		if argument == "-h" || argument == "--help" || argument == "--version" {
			return true
		}
	}
	return arguments[0] == "help" || arguments[0] == "version" || arguments[0] == "completion"
}

func credentialDisclosureInvocation(arguments []string) bool {
	// Match the command tokens in order so global flags may appear before or
	// between them. This remains intentionally narrower than an auth-command
	// denylist: auth status and future auth diagnostics are not blocked.
	wanted := [...]string{"auth", "get", "jwt"}
	matched := 0
	for _, argument := range arguments {
		if argument == wanted[matched] {
			matched++
			if matched == len(wanted) {
				return true
			}
		}
	}
	return false
}

func SHA256() [sha256.Size]byte {
	return policySHA256
}

func SHA256Hex() string {
	return hex.EncodeToString(policySHA256[:])
}
