// Package managedsandboxprofile defines the small, provider-independent
// authority that binds a workspace region to one immutable managed sandbox
// deployment profile.
package managedsandboxprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

const (
	RegionCN      = "cn"
	RegionBOE     = "boe"
	RegionI18NBD  = "i18n-bd"
	RegionI18NTT  = "i18n-tt"
	DefaultRegion = RegionI18NTT
)

var (
	regionSet = map[string]struct{}{
		RegionCN: {}, RegionBOE: {}, RegionI18NBD: {}, RegionI18NTT: {},
	}
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	profilePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Regions returns the stable public region order used by API and UI clients.
func Regions() []string {
	return []string{RegionCN, RegionBOE, RegionI18NBD, RegionI18NTT}
}

func ValidRegion(region string) bool {
	_, ok := regionSet[region]
	return ok
}

// ValidProfileID reports whether profileID is a canonical immutable profile
// identifier. It is exported so profile routers can validate map keys without
// constructing a synthetic Binding.
func ValidProfileID(profileID string) bool {
	return profilePattern.MatchString(profileID)
}

// Binding is the immutable routing authority frozen into a run. ProfileID is
// an operator-owned revision identity; changing any provider or network fact
// requires a new ProfileID and BindingSHA256.
type Binding struct {
	Region        string `json:"region"`
	ProfileID     string `json:"profileId"`
	BindingSHA256 string `json:"bindingSha256"`
	EnvironmentID string `json:"environmentId"`
}

func (binding Binding) Validate() error {
	if !ValidRegion(binding.Region) {
		return fmt.Errorf("managed sandbox region %q is unsupported", binding.Region)
	}
	if !ValidProfileID(binding.ProfileID) {
		return errors.New("managed sandbox profile ID must be canonical bounded text")
	}
	if !digestPattern.MatchString(binding.BindingSHA256) {
		return errors.New("managed sandbox binding SHA-256 must be a lowercase digest")
	}
	if !uuidPattern.MatchString(binding.EnvironmentID) || binding.EnvironmentID == "00000000-0000-0000-0000-000000000000" {
		return errors.New("managed sandbox environment ID must be a non-zero canonical lowercase UUID")
	}
	return nil
}

// Catalog is an immutable process-local projection of the validated
// production configuration. It deliberately contains no endpoint, proxy, or
// credential material: Core needs only the exact authority it freezes in a
// run.
type Catalog struct {
	defaultRegion string
	bindings      map[string]Binding
}

type CatalogDocument struct {
	DefaultRegion string    `json:"defaultRegion"`
	Bindings      []Binding `json:"bindings"`
}

func ParseCatalog(raw []byte) (*Catalog, error) {
	if len(raw) == 0 || len(raw) > 64*1024 {
		return nil, errors.New("managed sandbox profile catalog must contain between 1 and 65536 bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document CatalogDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode managed sandbox profile catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("managed sandbox profile catalog contains trailing data")
	}
	return NewCatalog(document.DefaultRegion, document.Bindings)
}

func NewCatalog(defaultRegion string, bindings []Binding) (*Catalog, error) {
	if defaultRegion != DefaultRegion {
		return nil, fmt.Errorf("managed sandbox default region must be %q", DefaultRegion)
	}
	if len(bindings) < 1 || len(bindings) > len(regionSet) {
		return nil, fmt.Errorf("managed sandbox catalog must contain between 1 and %d regions", len(regionSet))
	}
	byRegion := make(map[string]Binding, len(bindings))
	profiles := make(map[string]struct{}, len(bindings))
	environments := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if err := binding.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := byRegion[binding.Region]; duplicate {
			return nil, fmt.Errorf("managed sandbox region %q is repeated", binding.Region)
		}
		if _, duplicate := profiles[binding.ProfileID]; duplicate {
			return nil, fmt.Errorf("managed sandbox profile %q is repeated", binding.ProfileID)
		}
		if _, duplicate := environments[binding.EnvironmentID]; duplicate {
			return nil, fmt.Errorf("managed sandbox environment %q is repeated", binding.EnvironmentID)
		}
		byRegion[binding.Region] = binding
		profiles[binding.ProfileID] = struct{}{}
		environments[binding.EnvironmentID] = struct{}{}
	}
	if _, ok := byRegion[defaultRegion]; !ok {
		return nil, errors.New("managed sandbox default region has no active profile")
	}
	return &Catalog{defaultRegion: defaultRegion, bindings: byRegion}, nil
}

func (catalog *Catalog) DefaultRegion() string {
	if catalog == nil {
		return ""
	}
	return catalog.defaultRegion
}

func (catalog *Catalog) Resolve(region string) (Binding, bool) {
	if catalog == nil {
		return Binding{}, false
	}
	binding, ok := catalog.bindings[region]
	return binding, ok
}

func (catalog *Catalog) Bindings() []Binding {
	if catalog == nil {
		return nil
	}
	result := make([]Binding, 0, len(catalog.bindings))
	for _, region := range Regions() {
		if binding, installed := catalog.bindings[region]; installed {
			result = append(result, binding)
		}
	}
	return result
}
