package acp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

// SetModel stores the model id applied to future sessions and, when the
// agent advertises the model config option, applies it to the running
// session as well. modelOverride makes the choice outlive a session
// restart: session/load would otherwise report the model persisted at
// creation time and revert the user's switch.
func (a *Agent) SetModel(model string) {
	model = strings.TrimSpace(model)
	a.mu.Lock()
	a.model = model
	a.mu.Unlock()
	a.modelsMu.Lock()
	a.modelOverride = model
	a.modelsMu.Unlock()
	if model != "" {
		if sess := a.activeSession.Load(); sess != nil {
			sess.SetLiveModel(model)
		}
	}
	// Do not log the model value: CodeQL taints it through the generic
	// core.ModelSwitcher path from provider config that also carries keys.
	slog.Info("acp: model changed")
}

// GetModel returns the configured model. When nothing is configured it
// returns the model the latest session reported, so /model can show the
// id the agent is actually using. The running session still reports its
// own value via (*acpSession).GetModel.
func (a *Agent) GetModel() string {
	a.mu.RLock()
	model := strings.TrimSpace(a.model)
	a.mu.RUnlock()
	if model != "" {
		return model
	}
	return a.LastReportedModel()
}

// AvailableModels prefers options advertised by the latest session,
// then the configured "models" option, then a one-shot probe. Agents
// like droid only reveal their model catalog inside session/new, so
// without a session (or configured list) /model would show an empty
// list until the user sent a first message. The probe spawns a
// throwaway session in a temp dir once per agent lifetime and caches
// the result, mirroring agent/dsh.
func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	if cached := a.cachedModelOptionsSnapshot(); len(cached) > 0 {
		return cached
	}
	a.mu.RLock()
	configured := append([]core.ModelOption(nil), a.configuredModels...)
	a.mu.RUnlock()
	if len(configured) > 0 {
		return configured
	}
	return a.probeAvailableModels(ctx)
}

func (a *Agent) cachedModelOptionsSnapshot() []core.ModelOption {
	a.modelsMu.RLock()
	defer a.modelsMu.RUnlock()
	return append([]core.ModelOption(nil), a.cachedModelOptions...)
}

func (a *Agent) probeAvailableModels(ctx context.Context) []core.ModelOption {
	a.modelsProbeMu.Lock()
	defer a.modelsProbeMu.Unlock()
	// A concurrent probe or a session that started while we waited may
	// have filled the cache already.
	if cached := a.cachedModelOptionsSnapshot(); len(cached) > 0 {
		return cached
	}
	if a.modelsProbed {
		return nil
	}
	a.modelsProbed = true

	dir, err := os.MkdirTemp("", "cc-connect-acp-model-probe-")
	if err != nil {
		slog.Warn("acp: model probe workspace", "error", err)
		return nil
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, listSessionsProbeTimeout)
	defer cancel()

	id, current, models, err := a.ProbeModelOptions(probeCtx, dir)
	if id != "" {
		// Best effort: agents without sessionCapabilities.delete keep
		// the closed session in their list; noteCapabilities made the
		// unsupported case cheap.
		if delErr := a.DeleteSession(context.Background(), id); delErr != nil &&
			!errors.Is(delErr, errACPDeleteUnsupported) {
			slog.Debug("acp: model probe session was not removed", "session_id", id, "error", delErr)
		}
	}
	if err != nil {
		slog.Warn("acp: model probe failed", "error", err)
		return nil
	}
	a.reportModelOptions(current, models)
	return a.cachedModelOptionsSnapshot()
}

// parseConfiguredModels accepts the same shapes cc-connect already uses
// for a configured model list: []ModelOption, []string, or []any of
// strings / maps. Map keys follow provider config ("model", "alias")
// and the ACP select option ("value", "name", "description").
func parseConfiguredModels(v any) []core.ModelOption {
	switch x := v.(type) {
	case nil:
		return nil
	case []core.ModelOption:
		return append([]core.ModelOption(nil), x...)
	case []string:
		return modelOptionsFromStrings(x)
	case []any:
		var out []core.ModelOption
		for _, item := range x {
			if opt, ok := modelOptionFromAny(item); ok {
				out = append(out, opt)
			}
		}
		return out
	default:
		return nil
	}
}

func modelOptionsFromStrings(in []string) []core.ModelOption {
	var out []core.ModelOption
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, core.ModelOption{Name: s})
	}
	return out
}

func modelOptionFromAny(v any) (core.ModelOption, bool) {
	switch t := v.(type) {
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return core.ModelOption{}, false
		}
		return core.ModelOption{Name: t}, true
	case core.ModelOption:
		if strings.TrimSpace(t.Name) == "" {
			return core.ModelOption{}, false
		}
		return t, true
	case map[string]any:
		return modelOptionFromMap(t)
	case map[string]string:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = val
		}
		return modelOptionFromMap(m)
	default:
		return core.ModelOption{}, false
	}
}

func modelOptionFromMap(m map[string]any) (core.ModelOption, bool) {
	id := firstMapString(m, "value", "model", "modelId")
	nameWasID := false
	if id == "" {
		id = firstMapString(m, "name")
		nameWasID = id != ""
	}
	if id == "" {
		return core.ModelOption{}, false
	}
	desc := firstMapString(m, "desc", "description")
	if desc == "" && !nameWasID {
		desc = firstMapString(m, "name")
	}
	if desc == id {
		desc = ""
	}
	return core.ModelOption{
		Name:  id,
		Desc:  desc,
		Alias: firstMapString(m, "alias"),
	}, true
}

func firstMapString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		s, ok := m[k].(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}
