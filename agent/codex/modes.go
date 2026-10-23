package codex

import (
	"fmt"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

// codexModeSpec is the single source for mode aliases, UI and wire settings.
type codexModeSpec struct {
	core.PermissionModeInfo
	Sandbox        string
	ApprovalPolicy string
	Reviewer       string
	AppServerOnly  bool
	Legacy         bool
	Aliases        []string
}

var codexModes = []codexModeSpec{
	{
		PermissionModeInfo: core.PermissionModeInfo{Key: "suggest", Name: "Read Only", NameZh: "只读",
			Desc:   "Read-only sandbox; asks when approval is needed, already-permitted actions run",
			DescZh: "只读沙箱；需要审批时询问，已获许可的操作直接运行"},
		Sandbox: "read-only", ApprovalPolicy: "on-request", Reviewer: "user",
		Aliases: []string{"read-only", "readonly", "read_only"},
	},
	{
		PermissionModeInfo: core.PermissionModeInfo{Key: "workspace", Name: "Workspace", NameZh: "工作区",
			Desc:   "Workspace-write sandbox; asks when approval is needed",
			DescZh: "工作区可写沙箱；需要审批时询问"},
		Sandbox: "workspace-write", ApprovalPolicy: "on-request", Reviewer: "user", AppServerOnly: true,
		Aliases: []string{"workspace-write", "workspace_write"},
	},
	{
		PermissionModeInfo: core.PermissionModeInfo{Key: "auto-review", Name: "Approve for Me", NameZh: "自动审批",
			Desc:   "Workspace-write sandbox; Codex reviews approval requests automatically",
			DescZh: "工作区可写沙箱；Codex 自动审查审批请求"},
		Sandbox: "workspace-write", ApprovalPolicy: "on-request", Reviewer: "auto_review", AppServerOnly: true,
		Aliases: []string{"auto_review", "autoreview", "approve-for-me", "approve_for_me"},
	},
	{
		PermissionModeInfo: core.PermissionModeInfo{Key: "full-auto", Name: "Workspace, No Prompts (Legacy)", NameZh: "工作区免审批（兼容）",
			Desc:   "Workspace-write sandbox; never prompts, denies actions requiring approval",
			DescZh: "工作区可写沙箱；不弹审批，需要审批的操作直接拒绝"},
		Sandbox: "workspace-write", ApprovalPolicy: "never", Reviewer: "user", Legacy: true,
		Aliases: []string{"fullauto", "full_auto", "auto", "auto-edit", "autoedit", "auto_edit", "edit"},
	},
	{
		PermissionModeInfo: core.PermissionModeInfo{Key: "yolo", Name: "Full Access", NameZh: "完全访问",
			Desc:   "Bypass all approvals and sandbox (DANGEROUS)",
			DescZh: "跳过所有审批和沙箱（危险）"},
		Sandbox: "danger-full-access", ApprovalPolicy: "never", Reviewer: "user",
		Aliases: []string{"bypass", "dangerously-bypass"},
	},
}

func resolveCodexMode(raw string) (codexModeSpec, error) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" { // Omitted configuration retains the safe default.
		key = "suggest"
	}
	for _, spec := range codexModes {
		if key == spec.Key {
			return spec, nil
		}
		for _, alias := range spec.Aliases {
			if key == alias {
				return spec, nil
			}
		}
	}
	return codexModeSpec{}, fmt.Errorf("codex: unknown permission mode %q", raw)
}

func resolveCodexBackendMode(mode, backend string) (codexModeSpec, error) {
	spec, err := resolveCodexMode(mode)
	if err != nil {
		return codexModeSpec{}, err
	}
	if spec.AppServerOnly && backend != "app_server" {
		return codexModeSpec{}, fmt.Errorf("codex: mode %q requires backend=\"app_server\"; codex exec has no approval IPC; set backend=\"app_server\" or use suggest, full-auto, or yolo", spec.Key)
	}
	return spec, nil
}

func (a *Agent) ValidateMode(mode string) error {
	if strings.TrimSpace(mode) == "" {
		return fmt.Errorf("codex: permission mode must not be empty")
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, err := resolveCodexBackendMode(mode, a.backend)
	return err
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()
	var modes []core.PermissionModeInfo
	for _, spec := range codexModes {
		if spec.AppServerOnly && backend != "app_server" {
			continue
		}
		info := spec.PermissionModeInfo
		if spec.Key == "suggest" && backend != "app_server" {
			info.Desc = "Read-only sandbox; no prompts, denies actions requiring approval"
			info.DescZh = "只读沙箱；不弹审批，需要审批的操作直接拒绝"
		}
		modes = append(modes, info)
	}
	return modes
}

var _ core.ModeValidator = (*Agent)(nil)
