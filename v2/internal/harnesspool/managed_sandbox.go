package harnesspool

import (
	"errors"
	"regexp"
	"time"
)

var managedSandboxDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var managedPackIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}@v[1-9][0-9]{0,8}$`)

type ManagedSandboxLaunchSpec struct {
	EnvironmentID        string
	RuntimeProfileDigest string
	PackID               string
	PackSetDigest        string
	SkillSHA256          string
	SandboxTTL           time.Duration
	ActivityTTL          time.Duration
}

func validateManagedSandboxLaunch(scheduled ScheduledRunAttempt, spec ManagedSandboxLaunchSpec) error {
	if err := validateScheduledLaunchAuthority(scheduled); err != nil {
		return err
	}
	if err := validateUUIDIdentity("managed environment ID", spec.EnvironmentID); err != nil {
		return err
	}
	if !managedPackIDPattern.MatchString(spec.PackID) {
		return errors.New("managed sandbox pack ID must be canonical and versioned")
	}
	if !managedSandboxDigestPattern.MatchString(spec.RuntimeProfileDigest) ||
		!managedSandboxDigestPattern.MatchString(spec.PackSetDigest) ||
		!managedSandboxDigestPattern.MatchString(spec.SkillSHA256) {
		return errors.New("managed sandbox runtime, pack-set, and skill digests must be lowercase SHA-256")
	}
	if spec.SandboxTTL < 30*time.Second || spec.SandboxTTL > 24*time.Hour || spec.SandboxTTL%time.Second != 0 {
		return errors.New("managed sandbox TTL must be whole seconds between 30 seconds and 24 hours")
	}
	if spec.ActivityTTL < time.Second || spec.ActivityTTL > 24*time.Hour || spec.ActivityTTL%time.Second != 0 {
		return errors.New("managed sandbox activity TTL must be whole seconds between 1 second and 24 hours")
	}
	if spec.ActivityTTL > spec.SandboxTTL {
		return errors.New("managed sandbox activity TTL must not exceed the sandbox TTL")
	}
	return nil
}
