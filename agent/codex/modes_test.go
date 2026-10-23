package codex

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestResolveCodexMode_CanonicalAndAliases(t *testing.T) {
	for _, tc := range []struct {
		key, sandbox, approval, reviewer string
		appOnly, legacy                  bool
		aliases                          []string
	}{
		{"suggest", "read-only", "on-request", "user", false, false, []string{"", "suggest", "read-only", "readonly", "read_only"}},
		{"workspace", "workspace-write", "on-request", "user", true, false, []string{"workspace", "workspace-write", "workspace_write"}},
		{"auto-review", "workspace-write", "on-request", "auto_review", true, false, []string{"auto-review", "auto_review", "autoreview", "approve-for-me", "approve_for_me"}},
		{"full-auto", "workspace-write", "never", "user", false, true, []string{"full-auto", "fullauto", "full_auto", "auto", "auto-edit", "autoedit", "auto_edit", "edit"}},
		{"yolo", "danger-full-access", "never", "user", false, false, []string{"yolo", "bypass", "dangerously-bypass"}},
	} {
		for _, alias := range tc.aliases {
			t.Run(alias, func(t *testing.T) {
				spec, err := resolveCodexMode(" " + strings.ToUpper(alias) + " ")
				if err != nil {
					t.Fatal(err)
				}
				if spec.Key != tc.key || spec.Sandbox != tc.sandbox || spec.ApprovalPolicy != tc.approval || spec.Reviewer != tc.reviewer || spec.AppServerOnly != tc.appOnly || spec.Legacy != tc.legacy {
					t.Fatalf("resolve(%q) = %+v", alias, spec)
				}
			})
		}
	}
	if _, err := resolveCodexMode("workspaec"); err == nil {
		t.Fatal("unknown explicit mode silently defaulted")
	}
}

func TestPermissionModes_BackendCanonicalChoices(t *testing.T) {
	for _, backend := range []string{"exec", "app_server"} {
		a := &Agent{backend: backend}
		var keys []string
		for _, mode := range a.PermissionModes() {
			keys = append(keys, mode.Key)
			if err := a.ValidateMode(mode.Key); err != nil {
				t.Fatalf("listed mode %q invalid: %v", mode.Key, err)
			}
			if strings.Contains(mode.Desc, "every tool call") {
				t.Fatal("misleading on-request description")
			}
		}
		want := []string{"suggest", "full-auto", "yolo"}
		if backend == "app_server" {
			want = []string{"suggest", "workspace", "auto-review", "full-auto", "yolo"}
		}
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("%s modes = %v, want %v", backend, keys, want)
		}
	}
}

func TestCodexModeValidation_UnknownDoesNotMutate(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"exec", "app_server"} {
		for _, mode := range []string{"workspaec", "hybrid", " "} {
			a := &Agent{backend: backend, mode: "full-auto"}
			if err := a.ValidateMode(mode); err == nil {
				t.Fatalf("ValidateMode(%q) succeeded", mode)
			}
			a.SetMode(mode)
			if a.GetMode() != "full-auto" {
				t.Fatalf("invalid SetMode(%q) mutated mode", mode)
			}
			if strings.TrimSpace(mode) != "" {
				if _, err := New(map[string]any{"cmd": bin, "backend": backend, "mode": mode}); err == nil {
					t.Fatalf("New accepted unknown mode %q", mode)
				}
				a.mode = mode // Defensive session validation, even for stale state.
				if _, err := a.StartSession(context.Background(), ""); err == nil {
					t.Fatal("StartSession accepted unknown mode")
				}
			}
		}
		a, err := New(map[string]any{"cmd": bin, "backend": backend})
		if err != nil || a.(*Agent).GetMode() != "suggest" {
			t.Fatalf("omitted mode: %v, %v", a, err)
		}
	}
}

func TestExecBackend_RejectsInteractiveModesBeforeSessionStart(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"workspace", "workspace-write", "auto-review", "approve-for-me"} {
		t.Run(mode, func(t *testing.T) {
			a := &Agent{backend: "exec", mode: mode}
			check := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), `backend="app_server"`) || !strings.Contains(err.Error(), "no approval IPC") {
					t.Fatalf("expected actionable backend error, got %v", err)
				}
			}
			check(a.ValidateMode(mode))
			_, err := a.StartSession(context.Background(), "")
			check(err)
			_, err = New(map[string]any{"cmd": bin, "backend": "exec", "mode": mode})
			check(err)
			_, err = newCodexSession(context.Background(), bin, nil, t.TempDir(), "", "", mode, "", "", nil, "", "", "")
			check(err)
			_, err = (&codexSession{mode: mode}).buildExecArgs("hello", nil)
			check(err)
		})
	}
}

func TestExecBackend_LegacyAliasesKeepMappings(t *testing.T) {
	for _, alias := range []string{"full-auto", "auto-edit", "auto", "edit"} {
		for _, resumeID := range []string{"", "saved-thread"} {
			s, err := newCodexSession(context.Background(), "codex", nil, ".", "", "", alias, resumeID, "", nil, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			args, err := s.buildExecArgs("hello", nil)
			_ = s.Close()
			if err != nil || !containsSequence(args, []string{"-c", `approval_policy="never"`}) {
				t.Fatalf("%s: %v, %v", alias, args, err)
			}
			wantSandbox := []string{"--sandbox", "workspace-write"}
			if resumeID != "" {
				wantSandbox = []string{"-c", `sandbox_mode="workspace-write"`}
			}
			if !containsSequence(args, wantSandbox) {
				t.Fatalf("%s: sandbox changed: %v", alias, args)
			}
		}
	}
}
