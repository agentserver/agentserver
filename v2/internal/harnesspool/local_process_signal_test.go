//go:build linux || darwin

package harnesspool

import (
	"os"
	"os/signal"
)

func signalIgnore(signals ...os.Signal) {
	signal.Ignore(signals...)
}
