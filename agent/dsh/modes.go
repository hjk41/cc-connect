package dsh

import (
	"log/slog"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

const (
	modeDefault       = "default"
	modePlan          = "plan"
	modeYolo          = "yolo"
	envWorkspaceWrite = "workspace-write"
	envReadOnly       = "read-only"
	envFullAccess     = "danger-full-access"
)

// canonicalMode maps a cc-connect mode name onto the key shown in /mode
// and the DSH_PERMISSION_MODE value dsh reads at process start.
func canonicalMode(raw string) (key, env string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", modeDefault, "workspace-write", "workspace_write":
		return modeDefault, envWorkspaceWrite, true
	case modePlan, "read-only", "readonly", "read_only":
		return modePlan, envReadOnly, true
	case modeYolo, "bypasspermissions", "bypass", "danger-full-access", "danger_full_access":
		return modeYolo, envFullAccess, true
	default:
		return "", "", false
	}
}

// SetMode stores a permission mode for the next dsh process. dsh has no
// session/set_mode. The next message resumes the same session, and
// prepareResume writes the permission facts into the log before that
// process starts. DSH_PERMISSION_MODE still seeds a brand-new session.
func (a *Agent) SetMode(mode string) {
	if a == nil {
		return
	}
	if strings.TrimSpace(mode) == "" {
		a.mu.Lock()
		a.mode = ""
		a.mu.Unlock()
		return
	}
	key, _, ok := canonicalMode(mode)
	if !ok {
		slog.Warn("dsh: ignoring unknown permission mode", "mode", mode)
		return
	}
	a.mu.Lock()
	a.mode = key
	a.mu.Unlock()
	slog.Info("dsh: permission mode changed for the next session process", "mode", key)
}

// AppliesModeOnResume reports that a mode change takes effect on the next
// message of the current conversation, when that session is resumed.
func (a *Agent) AppliesModeOnResume() bool { return true }

// GetMode returns the mode the next process will use. Empty means the
// harness default, which /mode shows as "default".
func (a *Agent) GetMode() string {
	if a == nil {
		return modeDefault
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mode == "" {
		return modeDefault
	}
	return a.mode
}

// PermissionModes is the /mode menu. Descriptions cover English and
// Chinese; other languages use the English fields, which is all
// PermissionModeInfo can carry.
func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{
			Key:    modeDefault,
			Name:   "Default",
			NameZh: "默认",
			Desc:   "Workspace write; ask before tools that need approval",
			DescZh: "工作区可写；需要审批的工具会弹出确认",
		},
		{
			Key:    modePlan,
			Name:   "Plan / Read-only",
			NameZh: "计划 / 只读",
			Desc:   "Read-only sandbox (DSH_PERMISSION_MODE=read-only)",
			DescZh: "只读沙箱（DSH_PERMISSION_MODE=read-only）",
		},
		{
			Key:    modeYolo,
			Name:   "YOLO",
			NameZh: "完全访问",
			Desc:   "Full access; approval prompts are not shown",
			DescZh: "完全访问；不再弹出审批",
		},
	}
}
