package harnesspool

import (
	"strings"
	"testing"
	"time"
)

func TestManagedSandboxLaunchRejectsActivityBeyondSandboxLifetime(t *testing.T) {
	spec := poolTestManagedSandboxSpec()
	spec.SandboxTTL = 30 * time.Second
	spec.ActivityTTL = time.Minute
	scheduled := ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()}
	if err := validateManagedSandboxLaunch(scheduled, spec); err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("activity lifetime validation error = %v", err)
	}
}

func poolTestManagedSandboxSpec() ManagedSandboxLaunchSpec {
	return ManagedSandboxLaunchSpec{
		EnvironmentID:        "64000000-0000-4000-8000-000000000006",
		RuntimeProfileDigest: strings.Repeat("a", 64),
		PackID:               "lark-readonly@v1",
		PackSetDigest:        strings.Repeat("b", 64),
		SkillSHA256:          strings.Repeat("c", 64),
		SandboxTTL:           time.Hour,
		ActivityTTL:          time.Second,
	}
}
