package dsh

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

const modelProbeTimeout = 8 * time.Second

// AvailableModels prefers options from the latest live session, then a
// one-shot probe (session/new in a temp directory, then session/close and
// deletion of that probe session), then the static fallback list.
func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	if a == nil || a.Agent == nil {
		return staticModels()
	}
	if live := a.SessionModelOptions(); len(live) > 0 {
		return presentModels(live)
	}
	a.mu.Lock()
	if a.probed {
		cached := append([]core.ModelOption(nil), a.probeModels...)
		a.mu.Unlock()
		if len(cached) > 0 {
			return cached
		}
		return staticModels()
	}
	a.mu.Unlock()

	probed := a.probeModelCatalog(ctx)
	a.mu.Lock()
	a.probed = true
	a.probeModels = probed
	a.mu.Unlock()
	if len(probed) > 0 {
		return probed
	}
	return staticModels()
}

// ValidateModel accepts an opaque option value, a display name, or an
// inner model id. The returned id is what later sessions match against
// the live config option.
func (a *Agent) ValidateModel(ctx context.Context, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("dsh: model is required")
	}
	matched, ok := matchListedModel(model, a.AvailableModels(ctx))
	if !ok {
		return "", fmt.Errorf("dsh: unknown model %q", model)
	}
	return matched, nil
}

func (a *Agent) probeModelCatalog(ctx context.Context) []core.ModelOption {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, modelProbeTimeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "cc-connect-dsh-probe-")
	if err != nil {
		slog.Warn("dsh: model probe temp dir", "error", err)
		return nil
	}
	a.excludeProbe(dir, "")
	defer func() { _ = os.RemoveAll(dir) }()

	id, models, err := a.Agent.ProbeModelOptions(probeCtx, dir)
	if id != "" {
		a.excludeProbe(dir, id)
		if delErr := a.DeleteSession(context.Background(), id); delErr != nil {
			slog.Warn("dsh: model probe session was not removed", "session_id", id, "error", delErr)
		}
	}
	if err != nil {
		slog.Warn("dsh: model probe failed; using static model list", "error", err)
		return nil
	}
	return presentModels(models)
}

func staticModels() []core.ModelOption {
	return []core.ModelOption{
		{Name: "deepseek-flash", Desc: "DeepSeek-V4.1-Flash"},
		{Name: "deepseek-v4-pro", Desc: "DeepSeek-V4-Pro"},
		{Name: "deepseek-v4-flash", Desc: "deepseek-v4-flash"},
	}
}

func presentModels(opts []core.ModelOption) []core.ModelOption {
	seen := map[string]struct{}{}
	var out []core.ModelOption
	for _, o := range opts {
		raw := strings.TrimSpace(o.Name)
		if raw == "" {
			continue
		}
		name := raw
		if inner := innerModelID(raw); inner != "" {
			name = inner
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			name = raw
			key = strings.ToLower(name)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		desc := strings.TrimSpace(o.Desc)
		if strings.EqualFold(desc, name) {
			desc = ""
		}
		out = append(out, core.ModelOption{Name: name, Desc: desc, Alias: o.Alias})
	}
	return out
}

func matchListedModel(input string, opts []core.ModelOption) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}
	for _, o := range opts {
		if o.Name == input || strings.EqualFold(o.Name, input) {
			return o.Name, true
		}
	}
	for _, o := range opts {
		if strings.TrimSpace(o.Desc) != "" && strings.EqualFold(o.Desc, input) {
			return o.Name, true
		}
	}
	if inner := innerModelID(input); inner != "" {
		for _, o := range opts {
			if strings.EqualFold(o.Name, inner) {
				return o.Name, true
			}
		}
	}
	return "", false
}

func innerModelID(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") {
		return ""
	}
	var parts []string
	if json.Unmarshal([]byte(value), &parts) != nil || len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}
