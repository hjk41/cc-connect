package dsh

import (
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

// SetProviders replaces the provider list. The active name is cleared
// when it is no longer present.
func (a *Agent) SetProviders(providers []core.ProviderConfig) {
	if a == nil {
		return
	}
	cp := make([]core.ProviderConfig, len(providers))
	copy(cp, providers)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.providers = cp
	if a.active != "" && a.providerByNameLocked(a.active) == nil {
		a.active = ""
	}
}

// SetActiveProvider selects a provider by name. The API key is forwarded
// as DEEPSEEK_API_KEY and base_url as DEEPSEEK_BASE_URL on the next process.
func (a *Agent) SetActiveProvider(name string) bool {
	if a == nil {
		return false
	}
	name = strings.TrimSpace(name)
	a.mu.Lock()
	defer a.mu.Unlock()
	if name == "" {
		a.active = ""
		return true
	}
	if a.providerByNameLocked(name) == nil {
		return false
	}
	a.active = name
	return true
}

// GetActiveProvider returns a copy of the selected provider, or nil.
func (a *Agent) GetActiveProvider() *core.ProviderConfig {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.activeProviderLocked()
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// ListProviders returns a copy of the configured providers.
func (a *Agent) ListProviders() []core.ProviderConfig {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]core.ProviderConfig(nil), a.providers...)
}

func (a *Agent) activeProviderLocked() *core.ProviderConfig {
	if a.active == "" {
		return nil
	}
	return a.providerByNameLocked(a.active)
}

func (a *Agent) providerByNameLocked(name string) *core.ProviderConfig {
	for i := range a.providers {
		if strings.EqualFold(a.providers[i].Name, name) {
			return &a.providers[i]
		}
	}
	return nil
}
