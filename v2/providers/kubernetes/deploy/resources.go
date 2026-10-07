package deploy

import (
	"github.com/agentserver/agentserver/v2/internal/kubernetesresources"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const ControllerVersion = kubernetesresources.ControllerVersion

type Config = kubernetesresources.Config

func Resources(config Config) ([]*unstructured.Unstructured, error) {
	objects, err := kubernetesresources.Resources(config)
	if err != nil {
		return nil, err
	}
	result := make([]*unstructured.Unstructured, 0, len(objects))
	for _, obj := range objects {
		result = append(result, &unstructured.Unstructured{Object: obj})
	}
	return result, nil
}
