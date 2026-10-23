package codex

import (
	"encoding/json"
	"log/slog"

	"github.com/chenhg5/cc-connect/core"
)

var _ core.LiveModeSwitcher = (*appServerSession)(nil)

// SetLiveMode changes the permissions sent on the next turn/start. It does not
// affect an in-flight turn or send any requests to the app-server.
func (s *appServerSession) SetLiveMode(mode string) bool {
	spec, err := resolveCodexBackendMode(mode, "app_server")
	if err != nil {
		return false
	}

	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if !s.Alive() || s.CurrentSessionID() == "" || s.effectiveSandboxTier == "" {
		return false
	}

	var policy map[string]any
	switch spec.Sandbox {
	case s.effectiveSandboxTier:
		// Keep an explicit override: the thread may still be on its original
		// native tier if no turn has carried the policy yet.
		policy = cloneSandboxPolicy(s.turnSandboxPolicy)
	case "read-only":
		policy = map[string]any{"type": "readOnly", "networkAccess": false}
	case "danger-full-access":
		policy = map[string]any{"type": "dangerFullAccess"}
	case "workspace-write":
		if s.lastWorkspacePolicy == nil {
			// Never guess the configured roots, network access or tmp flags.
			return false
		}
		policy = cloneSandboxPolicy(s.lastWorkspacePolicy)
	default:
		return false
	}

	s.mode = spec.Key
	s.effectiveSandboxTier = spec.Sandbox
	s.turnSandboxPolicy = policy
	if policy != nil && spec.Sandbox == "workspace-write" {
		s.lastWorkspacePolicy = cloneSandboxPolicy(policy)
	}
	return true
}

func (s *appServerSession) applyThreadSandbox(state appServerPermissionState) {
	// Decoding makes a private copy of every field, including future nested
	// policy settings. Legacy string tiers do not provide a restorable policy.
	var sandbox any
	if len(state.Sandbox) != 0 {
		if err := json.Unmarshal(state.Sandbox, &sandbox); err != nil {
			slog.Warn("codex app-server: decode effective sandbox", "error", err)
		}
	}
	policy, _ := sandbox.(map[string]any)
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.effectiveSandboxTier = effectiveSandboxTier(state.Sandbox)
	if s.effectiveSandboxTier == "workspace-write" && policy != nil {
		s.lastWorkspacePolicy = cloneSandboxPolicy(policy)
	}
	s.turnSandboxPolicy = nil
}

// Policies contain JSON values only. Copy nested objects and arrays so no
// request payload shares mutable policy data with session state.
func cloneSandboxPolicy(policy map[string]any) map[string]any {
	if policy == nil {
		return nil
	}
	return cloneSandboxValue(policy).(map[string]any)
}

func cloneSandboxValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = cloneSandboxValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneSandboxValue(item)
		}
		return out
	default:
		return v
	}
}
