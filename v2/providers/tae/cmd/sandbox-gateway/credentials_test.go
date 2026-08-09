package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadByteCloudCredentialsReadsBoundedRegularFiles(t *testing.T) {
	directory := t.TempDir()
	accessPath := writeCredentialTestFile(t, directory, "access", "AK-example", 0o600)
	secretPath := writeCredentialTestFile(t, directory, "secret", "SK-super-secret", 0o400)
	credentials, err := loadByteCloudCredentials(accessPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.accessKeyID != "AK-example" || credentials.secretAccessKey != "SK-super-secret" {
		t.Fatal("credential files were not read exactly")
	}
}

func TestReadCredentialFileRejectsUnsafeMaterialWithoutLeakingIt(t *testing.T) {
	directory := t.TempDir()
	secret := "SK-do-not-leak"
	tests := []struct {
		name    string
		prepare func(*testing.T) string
	}{
		{name: "relative path", prepare: func(*testing.T) string { return "relative/secret" }},
		{name: "trailing newline", prepare: func(t *testing.T) string {
			return writeCredentialTestFile(t, directory, "newline", secret+"\n", 0o600)
		}},
		{name: "group writable", prepare: func(t *testing.T) string {
			return writeCredentialTestFile(t, directory, "writable", secret, 0o620)
		}},
		{name: "oversized", prepare: func(t *testing.T) string {
			return writeCredentialTestFile(t, directory, "oversized", strings.Repeat("x", int(credentialFileMaximumBytes)+1), 0o600)
		}},
		{name: "directory", prepare: func(*testing.T) string { return directory }},
		{name: "symlink", prepare: func(t *testing.T) string {
			target := writeCredentialTestFile(t, directory, "target", secret, 0o600)
			link := filepath.Join(directory, "link")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			return link
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := readCredentialFile(testCase.prepare(t), "test credential")
			if err == nil {
				t.Fatal("unsafe credential file was accepted")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("credential value leaked through error: %v", err)
			}
		})
	}
}

func TestLoadByteCloudCredentialsRejectsInvalidApplicationValues(t *testing.T) {
	directory := t.TempDir()
	for _, testCase := range []struct {
		name, access, secret string
	}{
		{name: "access delimiter", access: "AK/bad", secret: "SK-secret"},
		{name: "secret empty", access: "AK-example", secret: ""},
		{name: "secret surrounding whitespace", access: "AK-example", secret: " SK-secret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			accessPath := writeCredentialTestFile(t, directory, testCase.name+"-access", testCase.access, 0o600)
			secretPath := writeCredentialTestFile(t, directory, testCase.name+"-secret", testCase.secret, 0o600)
			_, err := loadByteCloudCredentials(accessPath, secretPath)
			if err == nil {
				t.Fatal("invalid application credential was accepted")
			}
			if testCase.secret != "" && strings.Contains(err.Error(), testCase.secret) {
				t.Fatalf("secret leaked through validation error: %v", err)
			}
		})
	}
}

func writeCredentialTestFile(t *testing.T, directory, name, contents string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(directory, strings.ReplaceAll(name, " ", "-"))
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
