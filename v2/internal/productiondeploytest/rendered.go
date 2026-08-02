// Package productiondeploytest exposes the rendered example deployment to
// command-package contract tests. It is test support only and is not imported
// by production binaries.
package productiondeploytest

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/agentserver/agentserver/v2/internal/productiondeploy"
)

type SecretReference struct {
	Name string
	Key  string
}

type Environment struct {
	Values  map[string]string
	Secrets map[string]SecretReference
}

func ExampleDeploymentEnvironment(component string) (Environment, error) {
	bundle, err := exampleBundle()
	if err != nil {
		return Environment{}, err
	}
	raw, found := bundle.File("30-runtime.json")
	if !found {
		return Environment{}, errors.New("rendered example has no runtime stage")
	}
	var list kubernetesList
	if err := json.Unmarshal(raw, &list); err != nil {
		return Environment{}, fmt.Errorf("decode rendered runtime stage: %w", err)
	}
	for _, item := range list.Items {
		if item.Kind != "Deployment" || item.Metadata.Name != component {
			continue
		}
		if len(item.Spec.Template.Spec.Containers) != 1 {
			return Environment{}, fmt.Errorf("deployment %s must have exactly one runtime container", component)
		}
		result := Environment{Values: map[string]string{}, Secrets: map[string]SecretReference{}}
		for _, value := range item.Spec.Template.Spec.Containers[0].Environment {
			if value.Name == "" {
				return Environment{}, fmt.Errorf("deployment %s contains an unnamed environment entry", component)
			}
			if _, duplicate := result.Values[value.Name]; duplicate {
				return Environment{}, fmt.Errorf("deployment %s repeats environment %s", component, value.Name)
			}
			if _, duplicate := result.Secrets[value.Name]; duplicate {
				return Environment{}, fmt.Errorf("deployment %s repeats environment %s", component, value.Name)
			}
			switch {
			case value.Value != "" && value.ValueFrom == nil:
				result.Values[value.Name] = value.Value
			case value.Value == "" && value.ValueFrom != nil && value.ValueFrom.SecretKeyRef.Name != "" && value.ValueFrom.SecretKeyRef.Key != "":
				result.Secrets[value.Name] = SecretReference{
					Name: value.ValueFrom.SecretKeyRef.Name,
					Key:  value.ValueFrom.SecretKeyRef.Key,
				}
			default:
				return Environment{}, fmt.Errorf("deployment %s environment %s has ambiguous authority", component, value.Name)
			}
		}
		return result, nil
	}
	return Environment{}, fmt.Errorf("rendered example has no deployment %s", component)
}

func ExampleWorkerDeployment() ([]byte, error) {
	bundle, err := exampleBundle()
	if err != nil {
		return nil, err
	}
	raw, found := bundle.File("00-foundation.json")
	if !found {
		return nil, errors.New("rendered example has no foundation stage")
	}
	var list kubernetesList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decode rendered foundation stage: %w", err)
	}
	for _, item := range list.Items {
		if item.Kind == "ConfigMap" && item.Data["worker-deployment.json"] != "" {
			return []byte(item.Data["worker-deployment.json"]), nil
		}
	}
	return nil, errors.New("rendered example has no worker deployment config")
}

func (environment Environment) Names() []string {
	result := make([]string, 0, len(environment.Values)+len(environment.Secrets))
	for name := range environment.Values {
		result = append(result, name)
	}
	for name := range environment.Secrets {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (environment Environment) Get(name string) string {
	return environment.Values[name]
}

func exampleBundle() (productiondeploy.Bundle, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return productiondeploy.Bundle{}, errors.New("locate production deploy test support")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	config, err := productiondeploy.LoadConfig(filepath.Join(root, "deploy", "production", "config.example.json"))
	if err != nil {
		return productiondeploy.Bundle{}, err
	}
	return productiondeploy.Render(config)
}

type kubernetesList struct {
	Items []struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Data map[string]string `json:"data"`
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Environment []struct {
							Name      string `json:"name"`
							Value     string `json:"value"`
							ValueFrom *struct {
								SecretKeyRef struct {
									Name string `json:"name"`
									Key  string `json:"key"`
								} `json:"secretKeyRef"`
							} `json:"valueFrom"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	} `json:"items"`
}
