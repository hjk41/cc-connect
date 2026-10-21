package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestReadOAuthTokens_UsesEffectiveCodexHome(t *testing.T) {
	for _, source := range []string{"explicit", "config", "provider", "session", "process", "default"} {
		t.Run(source, func(t *testing.T) {
			homes := make(map[string]string)
			for _, name := range []string{"explicit", "config", "provider", "session", "process", "default"} {
				home := t.TempDir()
				if name == "default" {
					t.Setenv("HOME", home)
					home = filepath.Join(home, ".codex")
					if err := os.MkdirAll(home, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				homes[name] = home
				if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"token","account_id":"`+name+`"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("CODEX_HOME", homes["process"])
			a := &Agent{activeIdx: -1}
			switch source {
			case "explicit":
				a.codexHome = homes["explicit"]
				fallthrough
			case "session":
				a.sessionEnv = []string{"CODEX_HOME=ignored", "CODEX_HOME=" + homes["session"]}
				fallthrough
			case "provider":
				a.providers = []core.ProviderConfig{{Env: map[string]string{"CODEX_HOME": homes["provider"]}}}
				a.activeIdx = 0
				fallthrough
			case "config":
				a.configEnv = []string{"CODEX_HOME=ignored", "CODEX_HOME=" + homes["config"]}
			case "default":
				t.Setenv("CODEX_HOME", "")
			}
			tokens, err := a.readOAuthTokens(os.ReadFile)
			if err != nil {
				t.Fatal(err)
			}
			if tokens.AccountID != source {
				t.Fatalf("read account %q, want %q", tokens.AccountID, source)
			}
		})
	}
}

func TestGetUsage_MissingAuthReportsEffectivePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	a := &Agent{configEnv: []string{"CODEX_HOME=" + home}}
	_, err := a.GetUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), filepath.Join(home, "auth.json")) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want missing effective auth.json path", err)
	}
}
