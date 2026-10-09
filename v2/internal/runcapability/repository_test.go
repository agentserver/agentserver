package runcapability

import (
	"encoding/base64"
	"encoding/json"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
	"strings"
	"testing"
)

func TestRepositoryCapabilityCannotEscapeManagedSession(t *testing.T) {
	c := developmentTestClaims(AudienceExecutorMCP)
	c.WorkspaceRepositoryID = c.SessionID
	repository := workspacerepository.Binding{CheckoutID: c.SessionID, Source: workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub.git", WorkingDirectory: "."}, SourceVersion: 1, Region: "sg", EnvironmentID: "50000000-0000-4000-8000-000000000005", ManagedSettingVersion: 1}
	raw, _ := json.Marshal(repository)
	c.WorkspaceRepositoryDescriptor = base64.RawURLEncoding.EncodeToString(raw)
	c.WorkspaceEnvironmentID = "50000000-0000-4000-8000-000000000005"
	c.WorkspaceEnvironmentVersion = 1
	c.WorkspaceRootSHA256 = strings.Repeat("1", 64)
	c.WorkspaceWorkingDirectory = "src"
	c.WorkspaceWorkingDirectoryVersion = 1
	c.ManagedSandboxEnvironmentID = c.WorkspaceEnvironmentID
	c.ManagedSandboxRegion = "sg"
	c.ManagedSandboxSettingVersion = 1
	codec, err := NewDevelopmentCodec(bytesOf(0x71, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Sign(c); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"session", "environment", "region", "model"} {
		invalid := c
		switch field {
		case "session":
			invalid.WorkspaceRepositoryID = "60000000-0000-4000-8000-000000000006"
		case "environment":
			invalid.ManagedSandboxEnvironmentID = "60000000-0000-4000-8000-000000000006"
		case "region":
			invalid.ManagedSandboxRegion = "i18n-tt"
		case "model":
			invalid = developmentTestClaims(AudienceLLMProxy)
			invalid.WorkspaceRepositoryID = invalid.SessionID
		}
		if _, err := codec.Sign(invalid); err == nil {
			t.Fatalf("accepted invalid repository %s", field)
		}
	}
}
