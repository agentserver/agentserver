package harnessinit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/networkguard"
)

const (
	networkGuardConfigVersion = 1
	maximumNetworkConfigBytes = 64 * 1024
)

type NetworkGuardDocument struct {
	Version  int                          `json:"version"`
	Table    string                       `json:"table"`
	Policies []NetworkGuardPolicyDocument `json:"policies"`
}

type NetworkGuardPolicyDocument struct {
	UID uint32                         `json:"uid"`
	TCP []NetworkGuardEndpointDocument `json:"tcp"`
}

type NetworkGuardEndpointDocument struct {
	Address string `json:"address"`
	Port    uint16 `json:"port"`
}

type NetworkGuardConfig struct {
	Table    string
	Policies []networkguard.UIDPolicy
}

func LoadNetworkGuardConfig(path string) (NetworkGuardConfig, error) {
	raw, err := readStableConfig(path, maximumNetworkConfigBytes)
	if err != nil {
		return NetworkGuardConfig{}, err
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 1024
	limits.MaxJSONDepth = 6
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumNetworkConfigBytes, limits); err != nil {
		return NetworkGuardConfig{}, fmt.Errorf("validate network guard JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document NetworkGuardDocument
	if err := decoder.Decode(&document); err != nil {
		return NetworkGuardConfig{}, fmt.Errorf("decode network guard config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return NetworkGuardConfig{}, errors.New("network guard config contains more than one JSON value")
		}
		return NetworkGuardConfig{}, fmt.Errorf("finish network guard config: %w", err)
	}
	if document.Version != networkGuardConfigVersion {
		return NetworkGuardConfig{}, fmt.Errorf("network guard config version must be %d", networkGuardConfigVersion)
	}
	if len(document.Policies) < 1 || len(document.Policies) > 32 {
		return NetworkGuardConfig{}, errors.New("network guard config must contain between 1 and 32 UID policies")
	}
	config := NetworkGuardConfig{Table: document.Table, Policies: make([]networkguard.UIDPolicy, len(document.Policies))}
	for policyIndex, policy := range document.Policies {
		if policy.UID == 0 || policy.UID == ^uint32(0) || len(policy.TCP) > 128 {
			return NetworkGuardConfig{}, fmt.Errorf("network guard policy %d has an invalid UID or endpoint count", policyIndex)
		}
		endpoints := make([]networkguard.Endpoint, len(policy.TCP))
		for endpointIndex, endpoint := range policy.TCP {
			address, err := netip.ParseAddr(endpoint.Address)
			if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() || endpoint.Port == 0 {
				return NetworkGuardConfig{}, fmt.Errorf("network guard policy %d endpoint %d is not a usable IPv4 TCP endpoint", policyIndex, endpointIndex)
			}
			endpoints[endpointIndex] = networkguard.Endpoint{Address: address, Port: endpoint.Port}
		}
		config.Policies[policyIndex] = networkguard.UIDPolicy{UID: policy.UID, AllowedEndpoints: endpoints}
	}
	return config, nil
}

func InstallNetworkGuard(config NetworkGuardConfig) error {
	return networkguard.Install(config.Table, config.Policies)
}

func readStableConfig(path string, maximum int64) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("configuration path must be absolute and clean")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect configuration: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o022 != 0 || before.Size() < 1 || before.Size() > maximum {
		return nil, errors.New("configuration must resolve to a bounded regular file not writable by group or other")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read configuration: %w", err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(raw)) != before.Size() {
		clear(raw)
		return nil, errors.New("configuration changed while it was being read")
	}
	return raw, nil
}
