package productiondeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderHelmChartLocksNamespaceHooksAndValues(t *testing.T) {
	document := validConfigDocument()
	document.Bootstrap.ExternalOIDCSubject = `{{ fail "template injection" }}`
	loaded, err := ValidateConfig(document)
	if err != nil {
		t.Fatal(err)
	}
	first, err := RenderHelmChart(loaded)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderHelmChart(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != len(requiredHelmChartFiles) || len(second.Files) != len(first.Files) {
		t.Fatalf("Helm chart file count = %d / %d", len(first.Files), len(second.Files))
	}
	for index := range first.Files {
		left, right := first.Files[index], second.Files[index]
		if left.Name != right.Name || left.SHA256 != right.SHA256 || !bytes.Equal(left.Content, right.Content) {
			t.Fatalf("Helm chart file %d is nondeterministic", index)
		}
	}

	foundation := parseHelmManifest(t, mustHelmFile(t, first, helmFoundationManifestFile))
	if countKind(foundation, "Namespace") != 0 {
		t.Fatal("Helm chart attempts to own the operator-managed Namespace")
	}
	migration := parseHelmManifest(t, mustHelmFile(t, first, helmMigrationManifestFile))
	bootstrap := parseHelmManifest(t, mustHelmFile(t, first, helmBootstrapManifestFile))
	assertHelmHook(t, migration, migrationComponent, "pre-install,pre-upgrade", "-10")
	assertHelmHook(t, bootstrap, bootstrapComponent, "post-install,post-upgrade", "0")
	migrationPodSpec := objectField(t, objectField(t, objectField(t, migration[0], "spec"), "template"), "spec")
	if stringField(t, migrationPodSpec, "serviceAccountName") != "default" || migrationPodSpec["automountServiceAccountToken"] != false {
		t.Fatal("migration hook depends on a chart-managed ServiceAccount or mounts an API token")
	}

	config := mustHelmFile(t, first, helmConfigFile)
	digest := sha256.Sum256(config)
	configSHA256 := hex.EncodeToString(digest[:])
	if !bytes.Contains(mustHelmFile(t, first, helmValuesFile), []byte(configSHA256)) ||
		!bytes.Contains(mustHelmFile(t, first, helmValuesSchemaFile), []byte(configSHA256)) ||
		!bytes.Contains(mustHelmFile(t, first, helmHelpersFile), []byte(configSHA256)) {
		t.Fatal("Helm values, schema, and guard do not lock the generated deployment config")
	}
	for _, templateName := range []string{
		helmFoundationTemplateFile, helmMigrationTemplateFile, helmBootstrapTemplateFile, helmRuntimeTemplateFile,
	} {
		template := mustHelmFile(t, first, templateName)
		if bytes.Contains(template, []byte("template injection")) || !bytes.Contains(template, []byte(".Files.Get")) {
			t.Fatalf("template %s evaluates rendered deployment content as Helm source", templateName)
		}
	}
	if !bytes.Contains(mustHelmFile(t, first, helmFoundationManifestFile), []byte("template injection")) {
		t.Fatal("test fixture did not reach the static manifest")
	}
}

func TestWriteHelmChartPublishesReadOnlyExactRetry(t *testing.T) {
	loaded, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	chart, err := RenderHelmChart(loaded)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "chart")
	for retry := 0; retry < 2; retry++ {
		if err := WriteHelmChart(chart, destination); err != nil {
			t.Fatalf("retry %d: %v", retry, err)
		}
	}
	t.Cleanup(func() {
		_ = filepath.Walk(destination, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	if err := os.Chmod(filepath.Join(destination, helmRuntimeTemplateFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteHelmChart(chart, destination); err == nil {
		t.Fatal("tampered production Helm chart was accepted on retry")
	}
}

func parseHelmManifest(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	documents := bytes.Split(raw, []byte("---\n"))
	resources := make([]map[string]any, 0, len(documents))
	for index, document := range documents {
		var resource map[string]any
		if err := json.Unmarshal(document, &resource); err != nil {
			t.Fatalf("decode Helm manifest document %d: %v", index, err)
		}
		resources = append(resources, resource)
	}
	return resources
}

func mustHelmFile(t *testing.T, chart HelmChart, name string) []byte {
	t.Helper()
	content, found := chart.File(name)
	if !found {
		t.Fatalf("Helm chart file %s not found", name)
	}
	return content
}

func assertHelmHook(t *testing.T, resources []map[string]any, component, hook, weight string) {
	t.Helper()
	if len(resources) != 1 || resources[0]["kind"] != "Job" {
		t.Fatalf("%s Helm hook is not one Job", component)
	}
	metadata := objectField(t, resources[0], "metadata")
	labels := objectField(t, metadata, "labels")
	annotations := objectField(t, metadata, "annotations")
	if stringField(t, labels, "app.kubernetes.io/name") != component ||
		stringField(t, annotations, "helm.sh/hook") != hook ||
		stringField(t, annotations, "helm.sh/hook-weight") != weight ||
		stringField(t, annotations, "helm.sh/hook-delete-policy") != "before-hook-creation,hook-succeeded" {
		t.Fatalf("%s Helm hook metadata = labels %#v annotations %#v", component, labels, annotations)
	}
}

func TestRequiredHelmChartFileListIsCanonical(t *testing.T) {
	for _, name := range requiredHelmChartFiles {
		if filepath.ToSlash(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			t.Fatalf("non-canonical required Helm chart path %q", name)
		}
	}
}
