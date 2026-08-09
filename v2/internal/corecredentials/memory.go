package corecredentials

import (
	"context"
	"errors"
	"sync"
)

// MemoryBindingStore is intentionally small and only intended for tests,
// local development, and the egress-authorizer development shell. Production
// uses the Core-backed store. It copies all byte slices at the boundary.
type MemoryBindingStore struct {
	mu       sync.RWMutex
	bindings map[string]Binding
}

func NewMemoryBindingStore() *MemoryBindingStore {
	return &MemoryBindingStore{bindings: make(map[string]Binding)}
}

func (store *MemoryBindingStore) Put(binding Binding) error {
	if store == nil {
		return errors.New("memory credential binding store is nil")
	}
	if binding.ID == "" || binding.WorkspaceID == "" || binding.Kind == "" {
		return errors.New("memory credential binding identity is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.bindings == nil {
		store.bindings = make(map[string]Binding)
	}
	binding.SealedSecret = append([]byte(nil), binding.SealedSecret...)
	binding.PublicMetadata = cloneJSON(binding.PublicMetadata)
	store.bindings[memoryBindingKey(binding.WorkspaceID, binding.Kind, binding.ID)] = binding
	return nil
}

func (store *MemoryBindingStore) Get(_ context.Context, workspaceID, kind, bindingID string) (Binding, error) {
	if store == nil {
		return Binding{}, errors.New("memory credential binding store is nil")
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	binding, ok := store.bindings[memoryBindingKey(workspaceID, kind, bindingID)]
	if !ok {
		return Binding{}, nil
	}
	binding.SealedSecret = append([]byte(nil), binding.SealedSecret...)
	binding.PublicMetadata = cloneJSON(binding.PublicMetadata)
	return binding, nil
}

func (store *MemoryBindingStore) List(_ context.Context, workspaceID, kind string) ([]BindingMetadata, error) {
	if store == nil {
		return nil, errors.New("memory credential binding store is nil")
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make([]BindingMetadata, 0)
	for _, binding := range store.bindings {
		if binding.WorkspaceID == workspaceID && binding.Kind == kind {
			result = append(result, binding.Metadata())
		}
	}
	// Deterministic output without importing a provider-specific ordering.
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j].ID < result[j-1].ID; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result, nil
}

func memoryBindingKey(workspaceID, kind, bindingID string) string {
	return workspaceID + "\x00" + kind + "\x00" + bindingID
}
