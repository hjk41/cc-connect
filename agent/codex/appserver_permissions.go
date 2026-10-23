package codex

import (
	"encoding/json"
	"log/slog"
)

// Keep structured policies/profile provenance intact for diagnostics, including
// newer protocol variants that this adapter does not select itself.
type appServerPermissionState struct {
	ApprovalPolicy          json.RawMessage `json:"approvalPolicy"`
	ApprovalsReviewer       json.RawMessage `json:"approvalsReviewer"`
	Sandbox                 json.RawMessage `json:"sandbox"`
	ActivePermissionProfile json.RawMessage `json:"activePermissionProfile"`
}

func (p appServerPermissionState) logEffectivePermissions(method string, requested codexModeSpec) {
	approval := permissionValue(p.ApprovalPolicy)
	reviewer := permissionValue(p.ApprovalsReviewer)
	sandbox := effectiveSandboxTier(p.Sandbox)
	slog.Info("codex app-server effective permissions", "method", method, "mode", requested.Key,
		"approvalPolicy", approval, "approvalsReviewer", reviewer,
		"sandbox", permissionValue(p.Sandbox), "activePermissionProfile", permissionValue(p.ActivePermissionProfile))
	for _, setting := range []struct{ field, want, got string }{
		{"approvalPolicy", requested.ApprovalPolicy, approval},
		{"approvalsReviewer", requested.Reviewer, reviewer},
		{"sandbox", requested.Sandbox, sandbox},
	} {
		if setting.got == "" {
			slog.Warn("codex app-server permission field missing or unsupported; effective setting cannot be verified",
				"method", method, "field", setting.field, "requested", setting.want)
		} else if setting.got != setting.want {
			slog.Warn("codex app-server effective permission mismatch", "method", method,
				"field", setting.field, "requested", setting.want, "effective", setting.got)
		}
	}
}

func permissionValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func effectiveSandboxTier(raw json.RawMessage) string {
	var policy struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &policy) != nil {
		// Also recognize legacy string representations.
		policy.Type = permissionValue(raw)
	}
	switch policy.Type {
	case "readOnly", "read-only":
		return "read-only"
	case "workspaceWrite", "workspace-write":
		return "workspace-write"
	case "dangerFullAccess", "danger-full-access":
		return "danger-full-access"
	default:
		return ""
	}
}
