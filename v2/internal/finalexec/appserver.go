package finalexec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	appServerProgramArgument   = "--program="
	appServerDirectoryArgument = "--directory="
	appServerUIDArgument       = "--expected-uid="
	appServerGIDArgument       = "--expected-gid="
)

// AppServerArguments is the complete, non-secret argv contract between a
// harness-worker and the final-exec trampoline. The app-server environment is
// deliberately not represented here because it may contain the llmproxy
// capability.
func AppServerArguments(program, directory string, expectedUID, expectedGID uint32) []string {
	return []string{
		appServerProgramArgument + program,
		appServerDirectoryArgument + directory,
		appServerUIDArgument + strconv.FormatUint(uint64(expectedUID), 10),
		appServerGIDArgument + strconv.FormatUint(uint64(expectedGID), 10),
	}
}

// ExecuteAppServer parses the closed-world trampoline argv and atomically
// replaces the process with stock `codex app-server`. It returns only on
// validation or exec failure.
func ExecuteAppServer(arguments, environment []string) error {
	config, err := appServerConfig(arguments, environment)
	if err != nil {
		return err
	}
	return Execute(config)
}

func appServerConfig(arguments, environment []string) (Config, error) {
	if len(arguments) != 4 {
		return Config{}, errors.New("app-server final exec requires exactly four launcher arguments")
	}
	program, ok := strings.CutPrefix(arguments[0], appServerProgramArgument)
	if !ok || program == "" {
		return Config{}, errors.New("app-server final exec program argument is invalid")
	}
	directory, ok := strings.CutPrefix(arguments[1], appServerDirectoryArgument)
	if !ok || directory == "" {
		return Config{}, errors.New("app-server final exec directory argument is invalid")
	}
	uid, err := parseCanonicalIdentity(arguments[2], appServerUIDArgument, "uid")
	if err != nil {
		return Config{}, err
	}
	gid, err := parseCanonicalIdentity(arguments[3], appServerGIDArgument, "gid")
	if err != nil {
		return Config{}, err
	}
	if environment == nil {
		return Config{}, errors.New("app-server final exec environment must be explicit")
	}
	return Config{
		Program:     program,
		Arguments:   []string{"app-server", "--listen", "stdio://", "--strict-config"},
		Directory:   directory,
		Environment: append([]string(nil), environment...),
		ExpectedUID: uid,
		ExpectedGID: gid,
	}, nil
}

func parseCanonicalIdentity(argument, prefix, label string) (uint32, error) {
	raw, ok := strings.CutPrefix(argument, prefix)
	if !ok || raw == "" {
		return 0, fmt.Errorf("app-server final exec %s argument is invalid", label)
	}
	parsed, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || strconv.FormatUint(parsed, 10) != raw || parsed == 0 || parsed == 1<<32-1 {
		return 0, fmt.Errorf("app-server final exec %s must be a canonical unprivileged identity", label)
	}
	return uint32(parsed), nil
}
