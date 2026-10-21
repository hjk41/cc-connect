package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

var _ core.ModelValidator = (*Agent)(nil)

// ValidateModel rejects unknown selections before the engine saves them or
// closes the current session. Provider configuration can advertise custom
// model IDs and aliases that are absent from the local Codex catalog.
func (a *Agent) ValidateModel(ctx context.Context, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("codex: %w %q", core.ErrUnknownModel, model)
	}
	// A previously saved command mix-up must not become a trusted model.
	for _, mode := range a.PermissionModes() {
		if strings.EqualFold(mode.Key, model) {
			return "", fmt.Errorf("codex: %w %q", core.ErrUnknownModel, model)
		}
	}
	if provider := a.GetActiveProvider(); provider != nil && strings.EqualFold(provider.Model, model) {
		return provider.Model, nil
	}
	models := a.configuredModels()
	known := len(models) > 0
	if len(models) == 0 {
		models, known = a.availableModels(ctx)
	}
	for _, m := range models {
		if m.Name == model {
			return m.Name, nil
		}
	}
	for _, m := range models {
		if strings.EqualFold(m.Name, model) || (m.Alias != "" && strings.EqualFold(m.Alias, model)) {
			return m.Name, nil
		}
	}
	if !known {
		// Keep manual model IDs working when discovery is unavailable.
		return model, nil
	}
	return "", fmt.Errorf("codex: %w %q", core.ErrUnknownModel, model)
}
