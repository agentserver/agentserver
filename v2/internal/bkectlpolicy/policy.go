// Package bkectlpolicy pins the exact read-only bkectl command surface that
// may receive a workspace ByteCloud credential inside a managed TAE process.
// The list is generated from the pinned upstream aggregate skill metadata and
// reviewed as part of the AgentServer release. Unknown, auth, write, and risky
// invocations fail closed before Core returns credential material.
package bkectlpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	PackID         = "bkectl-readonly@v1"
	Executable     = "bkectl"
	CredentialKind = "bytecloud"
	CredentialHost = "cloud-i18n-sg.bytedance.net"
	SourceRevision = "d813842ab03b24f2ac7a5b374507c273f41cbf21"
	CLIVersion     = SourceRevision
	CLISHA256      = "0331eb9836e46034d07bc88c52fa7af738dff7ad0b2b83d85ffe7cd6dfb7a590"
	CLISizeBytes   = int64(34263166)

	SkillPackSHA256 = "b752c88857ca7035580e9a9d51e9c63a09fd1bda28ce290c4e71b351c3779e51"

	maximumArguments     = 512
	maximumArgumentBytes = 32 * 1024
	policyDomain         = "agentserver-v2/bkectl-readonly-policy/v1\x00"
)

var (
	ErrInvocationDenied = errors.New("managed bkectl invocation is outside the pinned read-only policy")
	policySHA256        = computePolicySHA256()
)

// CredentialRequired validates argv and reports whether it is one of the
// pinned read-only leaf commands. Help/version discovery is explicitly safe
// without a credential. Every other shape is denied rather than falling back
// to local auth or an interactive device flow inside the sandbox.
func CredentialRequired(arguments []string) (bool, error) {
	if len(arguments) > maximumArguments {
		return false, ErrInvocationDenied
	}
	for _, argument := range arguments {
		if argument == "" || len(argument) > maximumArgumentBytes || !utf8.ValidString(argument) ||
			strings.ContainsAny(argument, "\x00\r\n") {
			return false, ErrInvocationDenied
		}
		if argument == "--debug" || strings.HasPrefix(argument, "--debug=") ||
			argument == "--confirm-write" || strings.HasPrefix(argument, "--confirm-write=") {
			return false, ErrInvocationDenied
		}
	}
	if safeDiscoveryInvocation(arguments) {
		return false, nil
	}
	for _, command := range allowedCommandPaths {
		path := strings.Split(command, " ")
		if len(arguments) < len(path) {
			continue
		}
		matched := true
		for index := range path {
			if arguments[index] != path[index] {
				matched = false
				break
			}
		}
		if matched {
			return true, nil
		}
	}
	return false, ErrInvocationDenied
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

func SHA256() [sha256.Size]byte {
	return policySHA256
}

func SHA256Hex() string {
	return hex.EncodeToString(policySHA256[:])
}

func computePolicySHA256() [sha256.Size]byte {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(policyDomain))
	_, _ = hasher.Write([]byte(SourceRevision))
	_, _ = hasher.Write([]byte{0})
	for _, command := range allowedCommandPaths {
		_, _ = hasher.Write([]byte(command))
		_, _ = hasher.Write([]byte{0})
	}
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

// allowedCommandPaths is generated from
// skills/bkectl/references/command-surface.md at SourceRevision. Auth commands
// are deliberately excluded even when upstream classifies them as read,
// because auth get jwt would disclose the injected workspace JWT.
var allowedCommandPaths = []string{
	"bke application change-ticket get",
	"bke application change-ticket list",
	"bke application get",
	"bke application list",
	"bke application release-instance get",
	"bke application release-instance list",
	"bke cluster-template list",
	"bke component get",
	"bke component list",
	"bke kok cluster get",
	"bke kok cluster list",
	"bke kok cluster-definition get",
	"bke kok cluster-definition list",
	"bke kok logical-cluster get",
	"bke kok logical-cluster list",
	"bke kok unit-cluster get",
	"bke kok unit-cluster list",
	"bke operation task get",
	"bke operation task list",
	"bytebox event category list",
	"bytebox event list",
	"bytebox fault list",
	"bytebox host get",
	"bytebox host list",
	"bytebox host reinstall-option list",
	"bytebox host search",
	"bytebox package get",
	"bytebox package list",
	"bytepaas cluster get",
	"bytepaas cluster list",
	"bytepaas deployment get",
	"bytepaas deployment list",
	"bytepaas instance get",
	"bytepaas instance list",
	"bytepaas instance search",
	"bytepaas service get",
	"bytesd block-list get",
	"bytesd block-status get",
	"bytesd node-services get",
	"bytesd service lookup",
	"bytetree node get",
	"bytetree node list",
	"bytetree node parent list",
	"clickhouse datasource list",
	"clickhouse query get",
	"clickhouse query run",
	"clickhouse queue list",
	"clickhouse template list",
	"collie cluster get",
	"collie cluster list",
	"dpu host check",
	"dpu instance get",
	"dpu topology get",
	"fatal improvement get",
	"fatal improvement list",
	"fatal incident get",
	"fatal incident list",
	"fault ticket list",
	"gpu capacity accelerator get",
	"gpu capacity accelerator list",
	"gpu capacity cluster list",
	"gpu capacity dimension list",
	"gpu elastic-pool supply dimension list",
	"gpu elastic-pool supply list",
	"gpu fault event search",
	"hive datasource list",
	"hive query get",
	"hive query run",
	"hive queue list",
	"hive template list",
	"idcmetadata ip get",
	"idcmetadata vdc get",
	"idcmetadata vdc list",
	"idcmetadata vregion get",
	"idcmetadata vregion list",
	"k8s cluster-resource get",
	"k8s cluster-resource list",
	"k8s configmap get",
	"k8s configmap list",
	"k8s crs get",
	"k8s crs list",
	"k8s dw get",
	"k8s dw list",
	"k8s fcluster get",
	"k8s fcluster list",
	"k8s fdw diagnose",
	"k8s fdw get",
	"k8s fdw list",
	"k8s kcnr get",
	"k8s kcnr list",
	"k8s node event list",
	"k8s node get",
	"k8s node list",
	"k8s node observe",
	"k8s pod event list",
	"k8s pod get",
	"k8s pod list",
	"k8s pod observe",
	"k8s pod trace",
	"k8s podtombstone get",
	"k8s podtombstone list",
	"k8s pv get",
	"k8s pv list",
	"k8s pvc get",
	"k8s pvc list",
	"k8s qrr get",
	"k8s qrr list",
	"k8s queue get",
	"k8s queue list",
	"k8s supply-reservation get",
	"k8s supply-reservation list",
	"karl host availability filter-option list",
	"karl host availability get",
	"karl host availability snapshot get",
	"karl host availability summary get",
	"karl host availability trend get",
	"karl host event get",
	"karl host event list",
	"karl host get",
	"karl host stuck list",
	"karl host stuck states",
	"karl host trace",
	"karl job list",
	"merlin job diagnose",
	"merlin robust-run diagnose",
	"merlin robust-run get",
	"merlin robust-run list",
	"obs dashboard container get",
	"obs dashboard host get",
	"obs dashboard node get",
	"obs dashboard pod get",
	"obs event deployment search",
	"obs event dw search",
	"obs event lifecycle-controller search",
	"obs event node search",
	"obs event pod search",
	"obs metric bosun query",
	"obs metric bosun template list",
	"obs metric cluster get",
	"obs metric machine get",
	"obs metric node get",
	"obs metric pod get",
	"obs metric quota get",
	"obs sli machine get",
	"obs sli node get",
	"oncall agent records get",
	"oncall agent records list",
	"oncall duty instruction list",
	"oncall duty queue get",
	"pike cluster event search",
	"pike cluster get",
	"pike cluster list",
	"pike cluster-runtime get",
	"pike cluster-runtime list",
	"pike fault event search",
	"pike fault get",
	"pike fault list",
	"pike machine event search",
	"pike machine get",
	"pike machine list",
	"pike machine-status list",
	"pike operation event search",
	"pike operation get",
	"pike operation list",
	"pike policy get",
	"pike policy list",
	"pike resourcepool get",
	"pike resourcepool list",
	"quota cluster get",
	"quota cluster list",
	"quota cluster scheduled-capacity-history",
	"quota instance-model get",
	"quota instance-model list",
	"quota resource-group get",
	"quota resource-group list",
	"quota resource-pool get",
	"quota resource-pool list",
	"quota supply-domain capacity get",
	"quota supply-domain freeze get",
	"quota supply-domain get",
	"quota supply-domain list",
	"quota supply-domain package list",
	"resource cluster capacity list",
	"resource cluster federation list",
	"resource cluster socket list",
	"resource control-plane get",
	"resource machine get",
	"resource machine operation ticket get",
	"resource machine operation ticket list",
	"resource nodepool list",
	"resource package expand",
	"resource package list",
	"resource scheduling assess",
	"resource supply plan",
	"spacex agent records get",
	"spacex agent records list",
	"spacex athena-job diagnose",
	"spacex athena-job get",
	"spacex athena-job list",
	"spacex athena-job log",
	"spacex change-ticket get",
	"spacex change-ticket list",
	"spacex cmdb-topology psm get",
	"spacex cmdb-topology psm list",
	"spacex cmdb-topology psm machine list",
	"spacex health alarm definition get",
	"spacex health alarm definition list",
	"spacex health alarm mute-policy list",
	"spacex health alarm object get",
	"spacex health alarm object list",
	"spacex health alarm rule get",
	"spacex health alarm rule list",
	"spacex health alarm rule scope list",
	"spacex health alarm template get",
	"spacex health alarm template list",
	"spacex health alarm ticket get",
	"spacex health alarm ticket list",
	"spacex health observation detection-scenes get",
	"spacex health observation detection-scenes list",
	"spacex health observation indicator get",
	"spacex health observation indicator list",
	"spacex health observation monitormeasurement get",
	"spacex health observation monitormeasurement list",
	"spacex release-orchestration detail",
	"spacex release-orchestration pipeline get",
	"spacex release-orchestration pipeline list",
	"spacex release-orchestration progress",
	"spacex release-orchestration task get",
	"spacex release-orchestration task list",
	"spacex release-plan get",
	"spacex release-plan list",
	"spacex release-plan trace",
	"spacex sense-operation get",
	"spacex sense-operation list",
	"tao task get",
	"tao task list",
	"tcc app-config get",
	"tcc app-config list",
	"tcc config get",
	"tcc config list",
	"tck coordinator riskctl audit list",
	"tck eventserver object-config get",
	"tck task diagnose",
	"tck task get",
	"tck task list",
}
