// Package managedruntime defines the image/control-plane boundary for the
// minimal process that keeps a TAE managed sandbox revision alive.
package managedruntime

const (
	ExecutableImagePath = "usr/local/bin/agentserver-tae-runtime"
	ExecutablePath      = "/" + ExecutableImagePath
	PortEnvironment     = "_BYTEFAAS_RUNTIME_PORT"
	DefaultPort         = 8080
)
