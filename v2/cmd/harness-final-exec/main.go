// harness-final-exec is the deliberately tiny last process boundary between a
// per-attempt harness-worker and stock Codex. It accepts no secrets in argv;
// the explicit environment is inherited from the worker and then preserved by
// the final exec.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/agentserver/agentserver/v2/internal/finalexec"
)

type executeAppServerFunc func([]string, []string) error

func main() {
	os.Exit(run(os.Args[1:], os.Environ(), os.Stderr, finalexec.ExecuteAppServer))
}

func run(arguments, environment []string, stderr io.Writer, execute executeAppServerFunc) int {
	if stderr == nil || execute == nil {
		return 2
	}
	if err := execute(arguments, environment); err != nil {
		_, _ = fmt.Fprintf(stderr, "harness-final-exec: %v\n", err)
		return 1
	}
	return 0
}
