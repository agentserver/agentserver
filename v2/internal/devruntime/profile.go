// Package devruntime assembles an explicitly insecure development runtime
// bundle from already-downloaded, release-pinned stock Codex artifacts.
// It does not download artifacts or sign a production runtime release.
package devruntime

import (
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

const PlatformLinuxARM64 = stockruntime.PlatformLinuxARM64

func stockLinuxARM64Manifest() runtimelock.Manifest {
	return stockruntime.LinuxARM64Manifest()
}
