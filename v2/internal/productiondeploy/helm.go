package productiondeploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

const (
	helmChartName = "agentserver-v2"

	helmChartFile                      = "Chart.yaml"
	helmValuesFile                     = "values.yaml"
	helmValuesSchemaFile               = "values.schema.json"
	helmHelpersFile                    = "templates/_helpers.tpl"
	helmFoundationTemplateFile         = "templates/00-foundation.yaml"
	helmHydraMigrationTemplateFile     = "templates/05-hydra-migrate.yaml"
	helmMigrationTemplateFile          = "templates/10-migrate.yaml"
	helmHydraSetupTemplateFile         = "templates/15-hydra-client-setup.yaml"
	helmBootstrapTemplateFile          = "templates/20-bootstrap.yaml"
	helmManagedEnvironmentTemplateFile = "templates/22-managed-environment.yaml"
	helmTAENetworkProbeTemplateFile    = "templates/25-tae-network-probe.yaml"
	helmRuntimeTemplateFile            = "templates/30-runtime.yaml"
	helmFoundationManifestFile         = "files/manifests/00-foundation.json"
	helmHydraMigrationManifestFile     = "files/manifests/05-hydra-migrate.json"
	helmMigrationManifestFile          = "files/manifests/10-migrate.json"
	helmHydraSetupManifestFile         = "files/manifests/15-hydra-client-setup.json"
	helmBootstrapManifestFile          = "files/manifests/20-bootstrap.json"
	helmManagedEnvironmentManifestFile = "files/manifests/22-managed-environment.json"
	helmTAENetworkProbeManifestFile    = "files/manifests/25-tae-network-probe.json"
	helmRuntimeManifestFile            = "files/manifests/30-runtime.json"
	helmConfigFile                     = "files/production-config.json"
	helmChecksumsFile                  = "files/checksums.json"
)

type HelmChart struct {
	Files []RenderedFile
}

func (chart HelmChart) File(name string) ([]byte, bool) {
	for _, file := range chart.Files {
		if file.Name == name {
			return append([]byte(nil), file.Content...), true
		}
	}
	return nil, false
}

// RenderHelmChart creates an environment-specific, values-locked Helm chart
// from the same validated resource graph as Render. User-controlled deployment
// text is kept under files/ and returned with .Files.Get so Helm never evaluates
// it as template source.
func RenderHelmChart(config LoadedConfig) (HelmChart, error) {
	validated, err := ValidateConfig(config.Document)
	if err != nil {
		return HelmChart{}, fmt.Errorf("validate production Helm input: %w", err)
	}
	config = validated
	if managedExecutionActive(config.Document.Managed) {
		if err := validateManagedReleaseEvidence(config.Document); err != nil {
			return HelmChart{}, fmt.Errorf("production Helm chart requires evidence-backed managed executor config: %w", err)
		}
	}
	bundle, err := Render(config)
	if err != nil {
		return HelmChart{}, err
	}

	configContent, err := json.MarshalIndent(config.Document, "", "  ")
	if err != nil {
		return HelmChart{}, fmt.Errorf("encode production Helm config: %w", err)
	}
	configContent = append(configContent, '\n')
	configSHA256 := sha256Hex(configContent)
	managedLarkEnabledValue := managedLarkEnabled(config.Document.Managed)
	taeNetworkProbeAllowed := managedPolicyBootstrap(config.Document.Managed)

	foundation, err := helmResources(bundle, foundationFile)
	if err != nil {
		return HelmChart{}, err
	}
	withoutNamespace := foundation[:0]
	namespaceCount := 0
	for _, resource := range foundation {
		if resource["kind"] == "Namespace" {
			metadata, _ := resource["metadata"].(map[string]any)
			if metadata["name"] != config.Document.Namespace {
				return HelmChart{}, errors.New("production foundation contains an unexpected Namespace")
			}
			namespaceCount++
			continue
		}
		withoutNamespace = append(withoutNamespace, resource)
	}
	if namespaceCount != 1 || len(withoutNamespace) == 0 {
		return HelmChart{}, errors.New("production Helm chart requires exactly one externalized Namespace")
	}

	migration, err := helmResources(bundle, migrationFile)
	if err != nil {
		return HelmChart{}, err
	}
	if err := addHelmHook(migration, migrationComponent, "pre-install,pre-upgrade", "-10"); err != nil {
		return HelmChart{}, err
	}
	hydraMigration, err := helmResources(bundle, hydraMigrationFile)
	if err != nil {
		return HelmChart{}, err
	}
	if err := addHelmHook(hydraMigration, hydraMigrationComponent, "pre-install,pre-upgrade", "-20"); err != nil {
		return HelmChart{}, err
	}
	hydraSetup, err := helmResources(bundle, hydraSetupFile)
	if err != nil {
		return HelmChart{}, err
	}
	if err := addHelmHook(hydraSetup, hydraSetupComponent, "post-install,post-upgrade", "-10"); err != nil {
		return HelmChart{}, err
	}
	bootstrap, err := helmResources(bundle, bootstrapFile)
	if err != nil {
		return HelmChart{}, err
	}
	if err := addHelmHook(bootstrap, bootstrapComponent, "post-install,post-upgrade", "0"); err != nil {
		return HelmChart{}, err
	}
	var managedEnvironment []kubeObject
	if managedExecutionActive(config.Document.Managed) {
		managedEnvironment, err = helmResources(bundle, managedEnvironmentBootstrapFile)
		if err != nil {
			return HelmChart{}, err
		}
		if err := addHelmHook(managedEnvironment, managedEnvironmentBootstrapComponent, "post-install,post-upgrade", "10"); err != nil {
			return HelmChart{}, err
		}
	}
	runtime, err := helmResources(bundle, runtimeFile)
	if err != nil {
		return HelmChart{}, err
	}
	taeNetworkProbe, err := taeNetworkProbeResources(config)
	if err != nil {
		return HelmChart{}, err
	}

	manifestGroups := []struct {
		name      string
		resources []kubeObject
	}{
		{name: helmFoundationManifestFile, resources: withoutNamespace},
		{name: helmHydraMigrationManifestFile, resources: hydraMigration},
		{name: helmMigrationManifestFile, resources: migration},
		{name: helmHydraSetupManifestFile, resources: hydraSetup},
		{name: helmBootstrapManifestFile, resources: bootstrap},
	}
	if managedExecutionActive(config.Document.Managed) {
		manifestGroups = append(manifestGroups,
			struct {
				name      string
				resources []kubeObject
			}{name: helmManagedEnvironmentManifestFile, resources: managedEnvironment},
		)
	}
	manifestGroups = append(manifestGroups, struct {
		name      string
		resources []kubeObject
	}{name: helmTAENetworkProbeManifestFile, resources: taeNetworkProbe})
	manifestGroups = append(manifestGroups, struct {
		name      string
		resources []kubeObject
	}{name: helmRuntimeManifestFile, resources: runtime})
	manifestFiles := make([]RenderedFile, 0, len(manifestGroups)+1)
	for _, group := range manifestGroups {
		content, err := marshalKubernetesDocuments(group.resources)
		if err != nil {
			return HelmChart{}, fmt.Errorf("render Helm manifest %s: %w", group.name, err)
		}
		manifestFiles = append(manifestFiles, renderedFile(group.name, content))
	}
	manifestFiles = append(manifestFiles, renderedFile(helmConfigFile, configContent))

	checksums, err := renderHelmChecksums(configSHA256, manifestFiles)
	if err != nil {
		return HelmChart{}, err
	}

	files := []RenderedFile{
		renderedFile(helmChartFile, renderChartYAML(configSHA256, config.Document.Runtime.RuntimeManifestSHA256)),
		renderedFile(helmValuesFile, []byte(fmt.Sprintf(
			"deploymentConfigSHA256: \"%s\"\nmanagedLarkEnabled: %t\ntaeNetworkProbe:\n  enabled: false\n  policyRevision: \"\"\n",
			configSHA256, managedLarkEnabledValue,
		))),
		renderedFile(helmValuesSchemaFile, renderValuesSchema(configSHA256, managedLarkEnabledValue, taeNetworkProbeAllowed)),
		renderedFile(helmHelpersFile, renderHelmGuard(config.Document.Namespace, configSHA256, managedLarkEnabledValue, taeNetworkProbeAllowed)),
		renderedFile(helmFoundationTemplateFile, renderManifestTemplate(helmFoundationManifestFile)),
		renderedFile(helmHydraMigrationTemplateFile, renderManifestTemplate(helmHydraMigrationManifestFile)),
		renderedFile(helmMigrationTemplateFile, renderManifestTemplate(helmMigrationManifestFile)),
		renderedFile(helmHydraSetupTemplateFile, renderManifestTemplate(helmHydraSetupManifestFile)),
		renderedFile(helmBootstrapTemplateFile, renderManifestTemplate(helmBootstrapManifestFile)),
		renderedFile(helmTAENetworkProbeTemplateFile, renderTAENetworkProbeTemplate(config.Document.Namespace)),
	}
	if managedExecutionActive(config.Document.Managed) {
		files = append(files, renderedFile(helmManagedEnvironmentTemplateFile, renderManifestTemplate(helmManagedEnvironmentManifestFile)))
	}
	files = append(files, renderedFile(helmRuntimeTemplateFile, renderManifestTemplate(helmRuntimeManifestFile)))
	files = append(files, manifestFiles...)
	files = append(files, renderedFile(helmChecksumsFile, checksums))
	return HelmChart{Files: files}, nil
}

func helmResources(bundle Bundle, name string) ([]kubeObject, error) {
	raw, found := bundle.File(name)
	if !found {
		return nil, fmt.Errorf("production bundle is missing %s", name)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var list struct {
		APIVersion string       `json:"apiVersion"`
		Kind       string       `json:"kind"`
		Items      []kubeObject `json:"items"`
	}
	if err := decoder.Decode(&list); err != nil {
		return nil, fmt.Errorf("decode production bundle file %s: %w", name, err)
	}
	if list.APIVersion != "v1" || list.Kind != "List" || len(list.Items) == 0 {
		return nil, fmt.Errorf("production bundle file %s is not a non-empty Kubernetes List", name)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("production bundle file %s contains trailing JSON", name)
		}
		return nil, fmt.Errorf("finish production bundle file %s: %w", name, err)
	}
	return list.Items, nil
}

func addHelmHook(resources []kubeObject, component, hook, weight string) error {
	if len(resources) != 1 || resources[0]["kind"] != "Job" {
		return fmt.Errorf("Helm hook %s must contain exactly one Job", component)
	}
	metadata, ok := resources[0]["metadata"].(map[string]any)
	if !ok {
		return fmt.Errorf("Helm hook %s has no metadata", component)
	}
	labels, _ := metadata["labels"].(map[string]any)
	if labels["app.kubernetes.io/name"] != component {
		return fmt.Errorf("Helm hook %s has the wrong component label", component)
	}
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		annotations = make(map[string]any)
		metadata["annotations"] = annotations
	}
	for key, value := range map[string]string{
		"helm.sh/hook":               hook,
		"helm.sh/hook-weight":        weight,
		"helm.sh/hook-delete-policy": "before-hook-creation,hook-succeeded",
	} {
		if _, exists := annotations[key]; exists {
			return fmt.Errorf("Helm hook %s already defines %s", component, key)
		}
		annotations[key] = value
	}
	return nil
}

func marshalKubernetesDocuments(resources []kubeObject) ([]byte, error) {
	if len(resources) == 0 {
		return nil, errors.New("Helm manifest resource set must not be empty")
	}
	var output bytes.Buffer
	for index, resource := range resources {
		if index != 0 {
			output.WriteString("---\n")
		}
		content, err := json.MarshalIndent(resource, "", "  ")
		if err != nil {
			return nil, err
		}
		output.Write(content)
		output.WriteByte('\n')
	}
	return output.Bytes(), nil
}

func renderChartYAML(configSHA256, runtimeSHA256 string) []byte {
	version := "0.1.0-config.d" + configSHA256[:12]
	return []byte(fmt.Sprintf(`apiVersion: v2
name: %s
description: Environment-locked production deployment for agentserver v2
type: application
version: %s
appVersion: "2"
annotations:
  agentserver.dev/deployment-config-sha256: "%s"
  agentserver.dev/runtime-manifest-sha256: "%s"
`, helmChartName, version, configSHA256, runtimeSHA256))
}

func renderValuesSchema(configSHA256 string, managedLarkEnabled, taeNetworkProbeAllowed bool) []byte {
	probeProperties := map[string]any{
		"enabled":        map[string]any{"type": "boolean"},
		"policyRevision": map[string]any{"type": "string", "maxLength": 128},
	}
	probeSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"enabled", "policyRevision"},
		"properties":           probeProperties,
	}
	if taeNetworkProbeAllowed {
		probeSchema["allOf"] = []any{map[string]any{
			"if": map[string]any{
				"properties": map[string]any{"enabled": map[string]any{"const": true}},
				"required":   []string{"enabled"},
			},
			"then": map[string]any{"properties": map[string]any{
				"policyRevision": map[string]any{
					"type": "string", "pattern": `^[0-9A-Za-z][0-9A-Za-z._-]{0,127}$`,
				},
			}},
			"else": map[string]any{"properties": map[string]any{
				"policyRevision": map[string]any{"enum": []string{""}},
			}},
		}}
	} else {
		probeProperties["enabled"] = map[string]any{"type": "boolean", "enum": []bool{false}}
		probeProperties["policyRevision"] = map[string]any{"type": "string", "enum": []string{""}}
	}
	document := map[string]any{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"deploymentConfigSHA256", "managedLarkEnabled", "taeNetworkProbe"},
		"properties": map[string]any{
			"deploymentConfigSHA256": map[string]any{
				"type": "string", "enum": []string{configSHA256},
			},
			"managedLarkEnabled": map[string]any{
				"type": "boolean", "enum": []bool{managedLarkEnabled},
			},
			"taeNetworkProbe": probeSchema,
		},
	}
	content, _ := json.MarshalIndent(document, "", "  ")
	return append(content, '\n')
}

func renderHelmGuard(namespace, configSHA256 string, managedLarkEnabled, taeNetworkProbeAllowed bool) []byte {
	return []byte(fmt.Sprintf(`{{- define "agentserver-v2.guard" -}}
{{- if ne .Release.Namespace %q -}}
{{- fail (printf "agentserver v2 chart is locked to namespace %s, got %%s" .Release.Namespace) -}}
{{- end -}}
{{- if ne (toString (default "" .Values.deploymentConfigSHA256)) %q -}}
{{- fail "agentserver v2 deploymentConfigSHA256 does not match this generated chart" -}}
{{- end -}}
{{- if ne .Values.managedLarkEnabled %t -}}
{{- fail "agentserver v2 managedLarkEnabled does not match this generated chart" -}}
{{- end -}}
{{- $probeRevision := toString (default "" .Values.taeNetworkProbe.policyRevision) -}}
{{- if .Values.taeNetworkProbe.enabled -}}
{{- if not %t -}}
{{- fail "agentserver v2 TAE network probe is available only in the policy-bootstrap stage" -}}
{{- end -}}
{{- if not (regexMatch "^[0-9A-Za-z][0-9A-Za-z._-]{0,127}$" $probeRevision) -}}
{{- fail "agentserver v2 TAE network probe policy revision is invalid" -}}
{{- end -}}
{{- if regexMatch "(?i)(PENDING|REPLACE|TODO|TBD|EXAMPLE|PLACEHOLDER|CHANGEME|SAMPLE|DUMMY)" $probeRevision -}}
{{- fail "agentserver v2 TAE network probe requires an actual published policy revision" -}}
{{- end -}}
{{- else if ne $probeRevision "" -}}
{{- fail "agentserver v2 TAE network probe policy revision must be empty while disabled" -}}
{{- end -}}
{{- end -}}
`, namespace, namespace, configSHA256, managedLarkEnabled, taeNetworkProbeAllowed))
}

func renderManifestTemplate(path string) []byte {
	return []byte("{{- include \"agentserver-v2.guard\" . -}}\n{{ .Files.Get \"" + path + "\" }}\n")
}

func renderTAENetworkProbeTemplate(namespace string) []byte {
	return []byte(fmt.Sprintf(`{{- include "agentserver-v2.guard" . -}}
{{- if .Values.taeNetworkProbe.enabled }}
{{- $revision := toString .Values.taeNetworkProbe.policyRevision -}}
{{- $identity := printf "%%s\n%%s" .Values.deploymentConfigSHA256 $revision | sha256sum | trunc 12 -}}
{{- $jobName := printf "tae-network-probe-%%s" $identity -}}
{{- $inputName := printf "tae-network-probe-input-%%s" $identity -}}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ $inputName | quote }}
  namespace: %q
  labels:
    app.kubernetes.io/name: %q
    app.kubernetes.io/part-of: "agentserver-v2"
    app.kubernetes.io/managed-by: "agentserver-deploy"
    agentserver.dev/network: "managed"
  annotations:
    agentserver.dev/deployment-config-sha256: {{ .Values.deploymentConfigSHA256 | quote }}
immutable: true
data:
  policy-revision: {{ $revision | quote }}
---
{{ .Files.Get %q | replace %q $jobName | replace %q $inputName }}
{{- end }}
`, namespace, taeNetworkProbeComponent, helmTAENetworkProbeManifestFile,
		taeNetworkProbeJobPlaceholder, taeNetworkProbeInputPlaceholder))
}

func renderHelmChecksums(configSHA256 string, files []RenderedFile) ([]byte, error) {
	type entry struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	}
	type document struct {
		Version                int     `json:"version"`
		DeploymentConfigSHA256 string  `json:"deploymentConfigSha256"`
		Files                  []entry `json:"files"`
	}
	entries := make([]entry, len(files))
	for index, file := range files {
		entries[index] = entry{Name: file.Name, SHA256: file.SHA256}
	}
	slices.SortFunc(entries, func(left, right entry) int { return strings.Compare(left.Name, right.Name) })
	content, err := json.MarshalIndent(document{Version: 1, DeploymentConfigSHA256: configSHA256, Files: entries}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render Helm checksums: %w", err)
	}
	return append(content, '\n'), nil
}

func renderedFile(name string, content []byte) RenderedFile {
	return RenderedFile{Name: filepath.ToSlash(name), Content: content, SHA256: sha256Hex(content)}
}
