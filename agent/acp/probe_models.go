package acp

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

// ProbeModelOptions starts one session in cwd, reads the model catalog
// (configOptions category "model", falling back to the legacy models
// block), and calls session/close when that capability was advertised.
// It returns the probed session id along with the agent's current model
// id so callers can also learn what the default selection is. The caller
// owns cwd and any on-disk session the agent persisted for the id.
func (a *Agent) ProbeModelOptions(ctx context.Context, cwd string) (string, string, []core.ModelOption, error) {
	if a == nil {
		return "", "", nil, fmt.Errorf("acp: probe agent is nil")
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", "", nil, fmt.Errorf("acp: probe cwd is required")
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return "", "", nil, fmt.Errorf("acp: probe cwd: %w", err)
	}
	cwd = canonicalCwd(cwd)

	tr, stderrBuf, teardown, err := a.probeSpawn(ctx, cwd, nil)
	if err != nil {
		return "", "", nil, err
	}
	defer teardown()

	init, err := probeInitialize(ctx, tr)
	if err != nil {
		return "", "", nil, wrapProbeStderr(err, stderrBuf)
	}
	a.noteCapabilities(init)
	raw, err := tr.call(ctx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("acp: probe session/new: %w", err)
	}
	var sn acpNewSessionResult
	if err := json.Unmarshal(raw, &sn); err != nil {
		return "", "", nil, fmt.Errorf("acp: probe parse session/new: %w", err)
	}
	if strings.TrimSpace(sn.SessionID) == "" {
		return "", "", nil, fmt.Errorf("acp: probe session/new: empty sessionId")
	}
	var models []core.ModelOption
	var current string
	if sn.Models != nil {
		models = modelOptionsFromLegacy(sn.Models)
		current = strings.TrimSpace(sn.Models.CurrentModelID)
	}
	if opt, ok := findModelConfigOption(sn.ConfigOptions); ok {
		models = modelOptionsFromConfig(opt)
		if v := strings.TrimSpace(string(opt.CurrentValue)); v != "" {
			current = v
		}
	}
	if sessionCloseSupported(init) {
		closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, closeErr := tr.call(closeCtx, "session/close", map[string]any{"sessionId": sn.SessionID})
		cancel()
		if closeErr != nil {
			slog.Debug("acp: probe session/close failed", "session_id", sn.SessionID, "error", closeErr)
		}
	}
	return sn.SessionID, current, models, nil
}
