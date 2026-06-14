package nameresolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ConnectedEntry mirrors the JSON shape codex-exec-gateway's
// /api/exec-gateway/connected returns. Exported so in-process callers
// (sdk.Server) can supply a Fetcher closure that returns these without
// the resolver round-tripping through HTTP. Per v0.54.0, exe_id is
// returned by the API but stripped from anything we send to the LLM.
type ConnectedEntry struct {
	ExeID       string `json:"exe_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	LastSeenAt  string `json:"last_seen_at,omitempty"`
}

// Fetcher returns the current connected-executor set. Used by callers
// that already have the list in-process (codex-exec-gateway's SDK
// layer); HTTP-mode callers (codex-app-gateway) still use NewResolver.
type Fetcher func(ctx context.Context) ([]ConnectedEntry, error)

// Resolver maintains a workspace-scoped name → exe_id map by
// periodically refreshing from codex-exec-gateway's
// /api/exec-gateway/connected. Tools that take an environment_id
// (semantically a name) call Resolve to get the underlying exe_id for
// bridge.Pool.Get.
//
// Cache strategy:
//   - First Resolve populates the cache.
//   - Subsequent Resolves use the cache if its age is under cacheTTL.
//   - A Resolve miss forces an immediate refresh before erroring.
type Resolver struct {
	// url is the upstream HTTP endpoint that returns the connected-
	// executor list for this caller's workspace. Per the 2026-06-14
	// loopback removal, this is codex-exec-gateway's
	// /api/exec-gateway/connected — formerly it was the app-gateway
	// loopback /internal/connected, but the exec-gateway endpoint now
	// accepts cap-tokens (workspace_id comes from the token payload,
	// not a query string), so env-mcp can call it directly without
	// the loopback hop.
	url string
	// bearer is the workspace cap-token sent as Authorization: Bearer.
	// Same token env-mcp uses for the /bridge dial; one HMAC-signed
	// claim authorises both surfaces.
	bearer     string
	httpClient *http.Client
	fetcher    Fetcher // non-nil in in-process mode; takes precedence over url
	logger     *slog.Logger
	cacheTTL   time.Duration

	mu       sync.Mutex
	cache    map[string]string // name → exe_id
	cachedAt time.Time

	// sf coalesces concurrent refresh() calls into one in-flight
	// HTTP fetch — protects the upstream from N parallel tool calls
	// that all miss cache at once.
	sf singleflight.Group
}

const nameResolverCacheTTL = 10 * time.Second

// NewResolver constructs an HTTP-backed name resolver. endpointURL is
// the full URL the resolver GETs (e.g.
// "http://codex-exec-gateway:8087/api/exec-gateway/connected");
// bearer is the workspace cap-token presented as
// Authorization: Bearer. The endpoint must derive workspace_id from
// the bearer's payload — passing it as a query string is intentionally
// unsupported, since the pre-2026-06-14 design that did so was
// forgeable and required a loopback proxy hop.
func NewResolver(endpointURL, bearer string, logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Resolver{
		url:        endpointURL,
		bearer:     bearer,
		httpClient: &http.Client{Timeout: 3 * time.Second},
		logger:     logger,
		cacheTTL:   nameResolverCacheTTL,
		cache:      map[string]string{},
	}
}

// NewResolverWithFetcher constructs a Resolver that calls fetch instead
// of making an HTTP request. Use this from in-process callers that have
// the connected list directly (codex-exec-gateway SDK layer) to avoid a
// loopback HTTP hop and the associated workspace cap-token plumbing.
func NewResolverWithFetcher(fetch Fetcher, logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Resolver{
		fetcher:  fetch,
		logger:   logger,
		cacheTTL: nameResolverCacheTTL,
		cache:    map[string]string{},
	}
}

// fetch reads the current connected list, either via the Fetcher (if
// set) or by hitting the loopback /internal/connected endpoint. Returns
// the raw entries — caller decides whether to update the cache.
func (r *Resolver) fetch(ctx context.Context) ([]ConnectedEntry, error) {
	if r.fetcher != nil {
		return r.fetcher(ctx)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.bearer)
	req.Header.Set("Accept", "application/json")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var entries []ConnectedEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return entries, nil
}

// refresh fetches and overwrites the cache. Concurrent callers
// share a single in-flight HTTP request via singleflight, so a tight
// loop of Resolve() misses doesn't fan out N requests to the loopback
// endpoint. cachedAt is bumped on EVERY refresh attempt (success or
// fail) so a steady stream of misses on an unknown name still
// throttles to one fetch per cacheTTL.
func (r *Resolver) refresh(ctx context.Context) ([]ConnectedEntry, error) {
	v, err, _ := r.sf.Do("refresh", func() (any, error) {
		entries, err := r.fetch(ctx)
		// Bump cachedAt regardless — see comment above.
		r.mu.Lock()
		r.cachedAt = time.Now()
		if err == nil {
			fresh := make(map[string]string, len(entries))
			for _, e := range entries {
				if e.Name != "" {
					fresh[e.Name] = e.ExeID
				}
			}
			r.cache = fresh
		}
		r.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return entries, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]ConnectedEntry), nil
}

// Resolve returns the exe_id bound to name in the current workspace.
// If name isn't in the cache, refreshes once before reporting
// not-found.
func (r *Resolver) Resolve(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("env name required")
	}
	r.mu.Lock()
	exeID, ok := r.cache[name]
	stale := time.Since(r.cachedAt) > r.cacheTTL
	r.mu.Unlock()
	if ok && !stale {
		return exeID, nil
	}
	// Either cache miss or stale — refresh.
	if _, err := r.refresh(ctx); err != nil {
		// On refresh failure, fall back to whatever was in the cache.
		if ok {
			return exeID, nil
		}
		return "", fmt.Errorf("refresh: %w", err)
	}
	r.mu.Lock()
	exeID, ok = r.cache[name]
	r.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no environment named %q (run list_environments to see what's available)", name)
	}
	return exeID, nil
}

// LLMView returns the entries reshaped for the LLM (omits exe_id).
// Always refreshes to keep the LLM's view fresh.
func (r *Resolver) LLMView(ctx context.Context) ([]byte, error) {
	entries, err := r.refresh(ctx)
	if err != nil {
		return nil, err
	}
	type llmEntry struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		IsDefault   bool   `json:"is_default,omitempty"`
		LastSeenAt  string `json:"last_seen,omitempty"`
	}
	out := make([]llmEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, llmEntry{
			Name: e.Name, Description: e.Description,
			IsDefault: e.IsDefault, LastSeenAt: e.LastSeenAt,
		})
	}
	return json.Marshal(out)
}
