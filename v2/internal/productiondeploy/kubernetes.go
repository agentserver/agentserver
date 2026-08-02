package productiondeploy

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/agentserver/agentserver/v2/internal/harnessinit"
)

type kubeObject map[string]any

func marshalKubernetesList(items []kubeObject) ([]byte, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("Kubernetes resource list must not be empty")
	}
	content, err := json.MarshalIndent(kubeObject{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      items,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func metadata(name, namespace string, labels, annotations map[string]string) kubeObject {
	result := kubeObject{"name": name}
	if namespace != "" {
		result["namespace"] = namespace
	}
	if len(labels) != 0 {
		result["labels"] = labels
	}
	if len(annotations) != 0 {
		result["annotations"] = annotations
	}
	return result
}

func componentLabels(component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       component,
		"app.kubernetes.io/part-of":    "agentserver-v2",
		"app.kubernetes.io/managed-by": "agentserver-deploy",
		"agentserver.dev/network":      "managed",
	}
}

func selectorLabels(component string) map[string]string {
	return map[string]string{"app.kubernetes.io/name": component, "app.kubernetes.io/part-of": "agentserver-v2"}
}

func namespaceResource(config LoadedConfig) kubeObject {
	return kubeObject{
		"apiVersion": "v1", "kind": "Namespace",
		"metadata": metadata(config.Document.Namespace, "", map[string]string{
			"app.kubernetes.io/part-of": "agentserver-v2",
		}, nil),
	}
}

func serviceAccountResource(config LoadedConfig, name, roleARN string) kubeObject {
	annotations := map[string]string(nil)
	if roleARN != "" {
		annotations = map[string]string{"eks.amazonaws.com/role-arn": roleARN}
	}
	return kubeObject{
		"apiVersion": "v1", "kind": "ServiceAccount",
		"metadata":                     metadata(name, config.Document.Namespace, componentLabels(name), annotations),
		"automountServiceAccountToken": false,
	}
}

func configMapResource(config LoadedConfig, name string, data map[string]string) kubeObject {
	return kubeObject{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": metadata(name, config.Document.Namespace, map[string]string{
			"app.kubernetes.io/part-of":    "agentserver-v2",
			"app.kubernetes.io/managed-by": "agentserver-deploy",
		}, nil),
		"immutable": true,
		"data":      data,
	}
}

func projectedMaterialVolume(name, secretName, profile string) (kubeObject, error) {
	files, found := harnessinit.MaterialProfileFiles(profile)
	if !found || len(files) == 0 {
		return nil, fmt.Errorf("unknown or empty material profile %q", profile)
	}
	items := make([]any, len(files))
	for index, file := range files {
		items[index] = kubeObject{"key": file, "path": file, "mode": 256}
	}
	return kubeObject{
		"name": name,
		"projected": kubeObject{
			"defaultMode": 256,
			"sources":     []any{kubeObject{"secret": kubeObject{"name": secretName, "items": items}}},
		},
	}, nil
}

func emptyDirVolume(name, medium, sizeLimit string) kubeObject {
	spec := kubeObject{}
	if medium != "" {
		spec["medium"] = medium
	}
	if sizeLimit != "" {
		spec["sizeLimit"] = sizeLimit
	}
	return kubeObject{"name": name, "emptyDir": spec}
}

func configMapVolume(name, configMapName string, items map[string]string) kubeObject {
	projectedItems := make([]any, 0, len(items))
	keys := sortedKeys(items)
	for _, key := range keys {
		projectedItems = append(projectedItems, kubeObject{
			"key": key, "path": items[key], "mode": 292,
		})
	}
	return kubeObject{"name": name, "configMap": kubeObject{
		"name": configMapName, "defaultMode": 292, "items": projectedItems,
	}}
}

func materializeInitContainer(image, profile, sourceVolume, sourcePath, materialVolume, destination string, uid, gid uint32) kubeObject {
	return kubeObject{
		"name":    "materialize-" + profile,
		"image":   image,
		"command": []any{"/usr/local/bin/agentserver-init"},
		"args": []any{
			"materialize", "--profile=" + profile, "--source=" + sourcePath,
			"--destination=" + destination, "--uid=" + strconv.FormatUint(uint64(uid), 10),
			"--gid=" + strconv.FormatUint(uint64(gid), 10),
		},
		"securityContext": initSecurityContext("CHOWN"),
		"volumeMounts": []any{
			kubeObject{"name": sourceVolume, "mountPath": sourcePath, "readOnly": true},
			kubeObject{"name": materialVolume, "mountPath": "/var/run/agentserver"},
		},
	}
}

func networkGuardInitContainer(image string) kubeObject {
	return kubeObject{
		"name":            "install-network-guard",
		"image":           image,
		"command":         []any{"/usr/local/bin/agentserver-init"},
		"args":            []any{"install-network-guard", "--config=/etc/agentserver/network-guard.json"},
		"securityContext": initSecurityContext("NET_ADMIN"),
		"volumeMounts": []any{kubeObject{
			"name": "harness-config", "mountPath": "/etc/agentserver/network-guard.json",
			"subPath": "network-guard.json", "readOnly": true,
		}},
	}
}

func prepareHarnessDirectoriesInitContainer(image string) kubeObject {
	return kubeObject{
		"name":    "prepare-harness-directories",
		"image":   image,
		"command": []any{"/usr/local/bin/agentserver-init"},
		"args": []any{
			"prepare-harness-directories",
			"--runtime=/var/lib/agentserver/runtime",
			"--checkpoint=/var/lib/agentserver/checkpoint",
			"--scratch=/tmp",
			"--uid=" + strconv.FormatUint(uint64(PoolUID), 10),
			"--gid=" + strconv.FormatUint(uint64(PoolGID), 10),
		},
		"securityContext": initSecurityContext("CHOWN", "FOWNER"),
		"volumeMounts": []any{
			kubeObject{"name": "runtime", "mountPath": "/var/lib/agentserver/runtime"},
			kubeObject{"name": "checkpoint", "mountPath": "/var/lib/agentserver/checkpoint"},
			kubeObject{"name": "scratch", "mountPath": "/tmp"},
		},
	}
}

func initSecurityContext(capabilities ...string) kubeObject {
	add := make([]any, len(capabilities))
	for index, capability := range capabilities {
		add[index] = capability
	}
	return kubeObject{
		"runAsUser": 0, "runAsGroup": 0, "allowPrivilegeEscalation": false,
		"readOnlyRootFilesystem": true,
		"capabilities":           kubeObject{"drop": []any{"ALL"}, "add": add},
		"seccompProfile":         kubeObject{"type": "RuntimeDefault"},
	}
}

func runtimeSecurityContext(uid, gid uint32, capabilities ...string) kubeObject {
	add := make([]any, len(capabilities))
	for index, capability := range capabilities {
		add[index] = capability
	}
	capabilitySpec := kubeObject{"drop": []any{"ALL"}}
	if len(add) != 0 {
		capabilitySpec["add"] = add
	}
	return kubeObject{
		"runAsUser": int64(uid), "runAsGroup": int64(gid), "runAsNonRoot": true,
		"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true,
		"capabilities":   capabilitySpec,
		"seccompProfile": kubeObject{"type": "RuntimeDefault"},
	}
}

func resources(document ContainerResourcesDocument) kubeObject {
	return kubeObject{
		"requests": kubeObject{"cpu": document.Requests.CPU, "memory": document.Requests.Memory},
		"limits":   kubeObject{"cpu": document.Limits.CPU, "memory": document.Limits.Memory},
	}
}

func productionNodeSelector() kubeObject {
	return kubeObject{
		"kubernetes.io/os":   "linux",
		"kubernetes.io/arch": "arm64",
	}
}

func imagePullSecrets(name string) []any {
	if name == "" {
		return []any{}
	}
	return []any{kubeObject{"name": name}}
}

func valueEnvironment(name, value string) kubeObject {
	return kubeObject{"name": name, "value": value}
}

func secretEnvironment(name, secretName, key string) kubeObject {
	return kubeObject{"name": name, "valueFrom": kubeObject{
		"secretKeyRef": kubeObject{"name": secretName, "key": key},
	}}
}

func execProbe(address string) kubeObject {
	return kubeObject{
		"exec":           kubeObject{"command": []any{"/usr/local/bin/agentserver-probe", "tcp", "--address=" + address}},
		"timeoutSeconds": 3, "periodSeconds": 5, "failureThreshold": 6,
	}
}

func hostAliases(entries map[string]string) []any {
	byIP := make(map[string][]string)
	for hostname, address := range entries {
		byIP[address] = append(byIP[address], hostname)
	}
	addresses := sortedKeys(byIP)
	result := make([]any, 0, len(addresses))
	for _, address := range addresses {
		hostnames := byIP[address]
		slicesSortStrings(hostnames)
		values := make([]any, len(hostnames))
		for index, hostname := range hostnames {
			values[index] = hostname
		}
		result = append(result, kubeObject{"ip": address, "hostnames": values})
	}
	return result
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slicesSortStrings(keys)
	return keys
}

func slicesSortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
