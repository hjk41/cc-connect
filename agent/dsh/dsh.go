// Package dsh integrates the DeepSeek Harness CLI (`dsh`) as a first-class
// cc-connect agent.
//
// dsh speaks ACP v1 over stdio when started with `--profile acp`. This
// package embeds *acp.Agent for the transport and adds harness-specific
// behavior: opaque model ids, reasoning-effort aliases, permission modes
// via DSH_PERMISSION_MODE, on-disk session history, and the DeepSeek
// balance API. The API key is read from the environment and is never
// stored or logged.
package dsh

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/chenhg5/cc-connect/agent/acp"
	"github.com/chenhg5/cc-connect/core"
)

func init() {
	core.RegisterAgent("dsh", New)
}

var (
	_ core.ModelSwitcher                   = (*Agent)(nil)
	_ core.ModelValidator                  = (*Agent)(nil)
	_ core.ReasoningEffortSwitcher         = (*Agent)(nil)
	_ core.ResumeReasoningApplier          = (*Agent)(nil)
	_ core.ModeSwitcher                    = (*Agent)(nil)
	_ core.ResumeModeApplier               = (*Agent)(nil)
	_ core.ProviderSwitcher                = (*Agent)(nil)
	_ core.SessionEnvInjector              = (*Agent)(nil)
	_ core.PlatformPromptInjector          = (*Agent)(nil)
	_ core.SystemPromptSupporter           = (*Agent)(nil)
	_ core.MemoryFileProvider              = (*Agent)(nil)
	_ core.SkillProvider                   = (*Agent)(nil)
	_ core.HistoryProvider                 = (*Agent)(nil)
	_ core.SessionDeleter                  = (*Agent)(nil)
	_ core.SessionTitleProvider            = (*Agent)(nil)
	_ core.SessionIDValidator              = (*Agent)(nil)
	_ core.WorkDirSwitcher                 = (*Agent)(nil)
	_ core.UsageReporter                   = (*Agent)(nil)
	_ core.AgentDoctorInfo                 = (*Agent)(nil)
	_ core.AgentOptsProvider               = (*Agent)(nil)
	_ core.WorkspaceAgentOptionSnapshotter = (*Agent)(nil)
)

// Agent embeds *acp.Agent. Methods defined here shadow the embedded
// ones where DeepSeek Harness needs its own behavior.
type Agent struct {
	*acp.Agent

	mu          sync.Mutex
	profile     string
	mode        string
	dshHome     string
	configEnv   map[string]string
	providers   []core.ProviderConfig
	active      string
	probed      bool
	probeModels []core.ModelOption
	excludedCwd []string
	excludedID  []string
	balanceURL  string
}

// Name is the stable config type.
func (a *Agent) Name() string { return "dsh" }

// CLIBinaryName is the real dsh binary. Doctor looks this name up on PATH.
func (a *Agent) CLIBinaryName() string { return "dsh" }

// CLIDisplayName is the label shown by /doctor.
func (a *Agent) CLIDisplayName() string { return "DeepSeek Harness" }

// HasSystemPromptSupport is false. dsh ACP has no system-prompt channel.
// The engine writes cc-connect instructions into the project AGENTS.md,
// which dsh loads as workspace instructions.
func (a *Agent) HasSystemPromptSupport() bool { return false }

// SetPlatformPrompt prefixes the first prompt of a newly created session
// with platform formatting instructions. Resumed sessions are unchanged.
func (a *Agent) SetPlatformPrompt(prompt string) {
	if a != nil && a.Agent != nil {
		a.Agent.SetPromptPrefix(prompt)
	}
}

// New builds a DeepSeek Harness agent.
//
// Options: cmd (default "dsh"), profile (default "acp", used only when
// args is unset), model (default "deepseek-flash"), reasoning_effort,
// mode, work_dir, env, dsh_home.
func New(opts map[string]any) (core.Agent, error) {
	opts = cloneOpts(opts)
	mode := strings.TrimSpace(stringOpt(opts["mode"]))
	effort := strings.TrimSpace(stringOpt(opts["reasoning_effort"]))
	dshHome := strings.TrimSpace(stringOpt(opts["dsh_home"]))
	profile := strings.TrimSpace(stringOpt(opts["profile"]))
	delete(opts, "mode")
	delete(opts, "reasoning_effort")
	delete(opts, "dsh_home")
	delete(opts, "profile")
	applyDefaults(opts, profile)

	raw, err := acp.New(opts)
	if err != nil {
		return nil, err
	}
	base, ok := raw.(*acp.Agent)
	if !ok {
		return raw, nil
	}
	agent := &Agent{
		Agent:     base,
		profile:   defaultProfile(profile),
		dshHome:   dshHome,
		configEnv: envMap(opts["env"]),
	}
	agent.FallbackReasoningEfforts = []string{"off", "low", "high", "max"}
	agent.ShowModelDisplayName = true
	agent.SpawnEnv = agent.spawnEnv
	agent.BeforeResume = agent.prepareResume
	if mode != "" {
		agent.SetMode(mode)
	}
	if effort != "" {
		agent.SetReasoningEffort(effort)
	}
	return agent, nil
}

func applyDefaults(opts map[string]any, profile string) {
	if !cmdConfigured(opts) {
		opts["cmd"] = "dsh"
	}
	if _, ok := opts["args"]; !ok {
		opts["args"] = []string{"--profile", defaultProfile(profile)}
	}
	if existing, _ := opts["display_name"].(string); strings.TrimSpace(existing) == "" {
		opts["display_name"] = "DeepSeek Harness"
	}
	if existing, _ := opts["model"].(string); strings.TrimSpace(existing) == "" {
		opts["model"] = "deepseek-flash"
	}
}

func defaultProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "acp"
	}
	return profile
}

func cmdConfigured(opts map[string]any) bool {
	cmd, _ := core.ParseCmdOpts(opts, "")
	return cmd != ""
}

func cloneOpts(opts map[string]any) map[string]any {
	if opts == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(opts))
	for k, v := range opts {
		out[k] = v
	}
	return out
}

func stringOpt(v any) string {
	s, _ := v.(string)
	return s
}

func envMap(v any) map[string]string {
	switch m := v.(type) {
	case map[string]string:
		out := make(map[string]string, len(m))
		for k, val := range m {
			out[k] = val
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(m))
		for k, val := range m {
			s, ok := val.(string)
			if ok {
				out[k] = s
			}
		}
		return out
	default:
		return nil
	}
}

// WorkspaceAgentOptions exports the settings a workspace clone needs.
func (a *Agent) WorkspaceAgentOptions() map[string]any {
	var opts map[string]any
	if a != nil && a.Agent != nil {
		opts = a.Agent.WorkspaceAgentOptions()
	}
	if opts == nil {
		opts = map[string]any{}
	}
	if a == nil {
		return opts
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.profile != "" {
		opts["profile"] = a.profile
	}
	if a.mode != "" {
		opts["mode"] = a.mode
	}
	if a.dshHome != "" {
		opts["dsh_home"] = a.dshHome
	}
	return opts
}

// BaseOpts is the workspace snapshot. work_dir is omitted on purpose.
func (a *Agent) BaseOpts() map[string]any { return a.WorkspaceAgentOptions() }

// spawnEnv is applied to every dsh process. DSH_PERMISSION_MODE seeds a
// fresh session. A resumed session keeps the permission facts already in
// its log; prepareResume appends the records that differ before the
// process starts.
func (a *Agent) spawnEnv() []string {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	mode := a.mode
	if mode == "" {
		mode = modeDefault
	}
	_, envVal, ok := canonicalMode(mode)
	if !ok {
		envVal = envWorkspaceWrite
	}
	out := []string{"DSH_PERMISSION_MODE=" + envVal}
	if home := strings.TrimSpace(a.dshHome); home != "" {
		out = append(out, "DSH_HOME="+home)
	}
	if p := a.activeProviderLocked(); p != nil {
		if key := strings.TrimSpace(p.APIKey); key != "" {
			out = append(out, "DEEPSEEK_API_KEY="+key)
		}
		if base := strings.TrimSpace(p.BaseURL); base != "" {
			// dsh-llm-deepseek reads $DEEPSEEK_BASE_URL as the Messages API root.
			out = append(out, "DEEPSEEK_BASE_URL="+base)
		}
		for k, v := range p.Env {
			k = strings.TrimSpace(k)
			if k == "" || k == "DEEPSEEK_API_KEY" || k == "DEEPSEEK_BASE_URL" || k == "DSH_PERMISSION_MODE" {
				continue
			}
			out = append(out, k+"="+v)
		}
	}
	return out
}

func (a *Agent) excludeProbe(cwd, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.TrimSpace(cwd) != "" {
		a.excludedCwd = append(a.excludedCwd, cwd)
	}
	if strings.TrimSpace(sessionID) != "" {
		a.excludedID = append(a.excludedID, sessionID)
	}
}

func (a *Agent) isExcluded(cwd, sessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.excludedID {
		if id != "" && id == sessionID {
			return true
		}
	}
	for _, dir := range a.excludedCwd {
		if cwd != "" && acp.SameCwd(dir, cwd) {
			return true
		}
	}
	return false
}

// StartSession is the embedded ACP start. Declared so a future wrapper
// can log the harness session without changing the call shape.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	if a == nil || a.Agent == nil {
		return nil, fmt.Errorf("dsh: agent is not initialized")
	}
	return a.Agent.StartSession(ctx, sessionID)
}
