package executionbackend

import (
	"context"
	"errors"
	"fmt"
)

// Router selects a backend solely from the Core-frozen target kind. It does
// not fall back to another provider when a backend is missing or unavailable.
type Router struct {
	backends map[Kind]Backend
}

func NewRouter(backends ...Backend) (*Router, error) {
	if len(backends) == 0 {
		return nil, errors.New("at least one execution backend is required")
	}
	router := &Router{backends: make(map[Kind]Backend, len(backends))}
	for index, backend := range backends {
		if backend == nil {
			return nil, fmt.Errorf("execution backend %d is nil", index)
		}
		kind := backend.Kind()
		if err := kind.Validate(); err != nil {
			return nil, fmt.Errorf("execution backend %d: %w", index, err)
		}
		if router.backends[kind] != nil {
			return nil, fmt.Errorf("execution backend kind %q is registered more than once", kind)
		}
		router.backends[kind] = backend
	}
	return router, nil
}

func (router *Router) StartProcess(ctx context.Context, request StartProcessRequest) (Exchange, error) {
	if err := request.Validate(); err != nil {
		return nil, NewDispatchError(OutcomeNotSent, "invalid_request", err)
	}
	backend, err := router.backend(request.Target)
	if err != nil {
		return nil, err
	}
	return backend.StartProcess(ctx, request)
}

func (router *Router) SignalProcess(ctx context.Context, request SignalProcessRequest) (Exchange, error) {
	if err := request.Validate(); err != nil {
		return nil, NewDispatchError(OutcomeNotSent, "invalid_request", err)
	}
	backend, err := router.backend(request.Target)
	if err != nil {
		return nil, err
	}
	return backend.SignalProcess(ctx, request)
}

func (router *Router) ReadFile(ctx context.Context, request ReadFileRequest) (Exchange, error) {
	if err := request.Validate(); err != nil {
		return nil, NewDispatchError(OutcomeNotSent, "invalid_request", err)
	}
	backend, err := router.backend(request.Target)
	if err != nil {
		return nil, err
	}
	return backend.ReadFile(ctx, request)
}

func (router *Router) backend(target Target) (Backend, error) {
	if router == nil {
		return nil, NewDispatchError(OutcomeNotSent, "router_unavailable", errors.New("execution backend router is nil"))
	}
	backend := router.backends[target.Kind]
	if backend == nil {
		return nil, NewDispatchError(
			OutcomeNotSent,
			"backend_unavailable",
			fmt.Errorf("execution backend kind %q is not configured", target.Kind),
		)
	}
	return backend, nil
}
