package claudecode

import (
	"context"
	"fmt"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

var _ core.ModelValidator = (*Agent)(nil)

// ValidateModel catches permission-mode mix-ups before they are saved, while
// preserving manual model IDs without requiring network discovery.
func (a *Agent) ValidateModel(_ context.Context, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("claudecode: %w %q", core.ErrUnknownModel, model)
	}
	for _, mode := range a.PermissionModes() {
		if strings.EqualFold(mode.Key, model) {
			return "", fmt.Errorf("claudecode: %w %q", core.ErrUnknownModel, model)
		}
	}
	for _, alias := range []string{"yolo", "full-auto", "suggest", "auto-edit"} {
		if strings.EqualFold(alias, model) {
			return "", fmt.Errorf("claudecode: %w %q", core.ErrUnknownModel, model)
		}
	}
	for _, configured := range a.configuredModels() {
		if configured.Name == model || (configured.Alias != "" && configured.Alias == model) {
			return configured.Name, nil
		}
	}
	return model, nil
}
