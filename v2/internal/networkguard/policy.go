// Package networkguard installs the process-identity egress boundary used by
// the harness workload. Pod-level networking remains a second layer; these
// policies distinguish trusted worker and app-server UIDs inside one network
// namespace. Endpoints are explicit IPv4 destinations; Linux installation
// denies all IPv6 traffic for every managed UID.
package networkguard

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
)

var tableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type Endpoint struct {
	Address netip.Addr
	Port    uint16
}

type UIDPolicy struct {
	UID              uint32
	AllowedEndpoints []Endpoint
}

func normalize(tableName string, policies []UIDPolicy) ([]UIDPolicy, error) {
	if !tableNamePattern.MatchString(tableName) {
		return nil, fmt.Errorf("invalid nftables table name %q", tableName)
	}
	if len(policies) == 0 {
		return nil, errors.New("at least one UID egress policy is required")
	}
	if len(policies) > 32 {
		return nil, errors.New("UID egress policy count exceeds 32")
	}
	result := make([]UIDPolicy, len(policies))
	seenUIDs := make(map[uint32]struct{}, len(policies))
	for policyIndex, policy := range policies {
		if policy.UID == 0 || policy.UID == ^uint32(0) {
			return nil, fmt.Errorf("UID egress policy %d has invalid or privileged UID %d", policyIndex, policy.UID)
		}
		if _, duplicate := seenUIDs[policy.UID]; duplicate {
			return nil, fmt.Errorf("duplicate UID egress policy %d", policy.UID)
		}
		seenUIDs[policy.UID] = struct{}{}
		if len(policy.AllowedEndpoints) > 128 {
			return nil, fmt.Errorf("UID %d endpoint count exceeds 128", policy.UID)
		}
		endpoints := append([]Endpoint(nil), policy.AllowedEndpoints...)
		for endpointIndex, endpoint := range endpoints {
			if !endpoint.Address.IsValid() || !endpoint.Address.Is4() || endpoint.Address.IsUnspecified() || endpoint.Address.IsMulticast() {
				return nil, fmt.Errorf("UID %d endpoint %d has invalid IPv4 address %q", policy.UID, endpointIndex, endpoint.Address)
			}
			if endpoint.Port == 0 {
				return nil, fmt.Errorf("UID %d endpoint %d has port zero", policy.UID, endpointIndex)
			}
		}
		slices.SortFunc(endpoints, func(left, right Endpoint) int {
			if compared := left.Address.Compare(right.Address); compared != 0 {
				return compared
			}
			return int(left.Port) - int(right.Port)
		})
		endpoints = slices.Compact(endpoints)
		result[policyIndex] = UIDPolicy{UID: policy.UID, AllowedEndpoints: endpoints}
	}
	slices.SortFunc(result, func(left, right UIDPolicy) int {
		if left.UID < right.UID {
			return -1
		}
		if left.UID > right.UID {
			return 1
		}
		return 0
	})
	return result, nil
}
