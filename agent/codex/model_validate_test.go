package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

type modelValidationPlatform struct {
	sent []string
}

func (p *modelValidationPlatform) Name() string                    { return "test" }
func (p *modelValidationPlatform) Start(core.MessageHandler) error { return nil }
func (p *modelValidationPlatform) Stop() error                     { return nil }
func (p *modelValidationPlatform) Reply(_ context.Context, _ any, text string) error {
	p.sent = append(p.sent, text)
	return nil
}
func (p *modelValidationPlatform) Send(ctx context.Context, replyCtx any, text string) error {
	return p.Reply(ctx, replyCtx, text)
}

func isolateModelValidation(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	return home
}

func TestCmdModel_YoloRejectedBeforePersistence(t *testing.T) {
	isolateModelValidation(t)
	a := &Agent{model: "gpt-4.1", mode: "full-auto", activeIdx: -1}
	p := &modelValidationPlatform{}
	e := core.NewEngine("test", a, []core.Platform{p}, "", core.LangChinese)
	saved := false
	e.SetModelSaveFunc(func(string) error {
		saved = true
		return nil
	})
	e.ReceiveMessage(p, &core.Message{
		SessionKey: "test:user", Platform: "test", UserID: "user", ReplyCtx: "ctx",
		Content: "/model yolo",
	})
	if saved || a.GetModel() != "gpt-4.1" || a.GetMode() != "full-auto" {
		t.Fatalf("invalid /model changed state: saved=%v model=%q mode=%q", saved, a.GetModel(), a.GetMode())
	}
	reply := strings.Join(p.sent, "\n")
	if !strings.Contains(reply, "切换模型失败") || !strings.Contains(reply, "yolo") || !strings.Contains(reply, "/model") {
		t.Fatalf("expected immediate localized rejection and model-list hint, got %q", reply)
	}
}

func TestValidateModel_RejectsUnknownModels(t *testing.T) {
	home := isolateModelValidation(t)
	cache := `{"models":[{"slug":"gpt-4.1","visibility":"list","supported_in_api":true}]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &Agent{activeIdx: -1}
	validator, ok := any(a).(core.ModelValidator)
	if !ok {
		t.Fatal("Codex must validate model selections before they are saved")
	}
	for _, model := range []string{"", "yolo", "full-auto", "missing-model"} {
		t.Run(model, func(t *testing.T) {
			if got, err := validator.ValidateModel(context.Background(), model); err == nil {
				t.Fatalf("ValidateModel(%q) = %q, want error", model, got)
			}
		})
	}
}

func TestValidateModel_FallbackPreservesManualModelsButRejectsModes(t *testing.T) {
	isolateModelValidation(t)
	a := &Agent{activeIdx: -1}
	for _, model := range []string{"gpt-future-codex", "vendor/custom-model"} {
		if got, err := a.ValidateModel(context.Background(), model); err != nil || got != model {
			t.Fatalf("manual model %q = %q, %v", model, got, err)
		}
	}
	for _, mode := range a.PermissionModes() {
		if got, err := a.ValidateModel(context.Background(), strings.ToUpper(mode.Key)); err == nil {
			t.Fatalf("permission mode %q accepted as model %q", mode.Key, got)
		}
	}
}

func TestValidateModel_RejectsPreviouslySavedPermissionMode(t *testing.T) {
	isolateModelValidation(t)
	a := &Agent{
		providers: []core.ProviderConfig{{Name: "custom", Model: "yolo"}},
		activeIdx: 0,
	}
	if got, err := a.ValidateModel(context.Background(), "yolo"); err == nil {
		t.Fatalf("previously saved permission mode accepted as model %q", got)
	}
}

func TestValidateModel_AcceptsCatalogAndProviderModels(t *testing.T) {
	home := isolateModelValidation(t)
	cache := `{"models":[{"slug":"gpt-future-codex","visibility":"list","supported_in_api":true}]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &Agent{activeIdx: -1}
	validator, ok := any(a).(core.ModelValidator)
	if !ok {
		t.Fatal("Codex must implement ModelValidator")
	}
	got, err := validator.ValidateModel(context.Background(), " GPT-FUTURE-CODEX ")
	if err != nil || got != "gpt-future-codex" {
		t.Fatalf("cached model = %q, %v", got, err)
	}
	a.SetProviders([]core.ProviderConfig{{
		Name:   "custom",
		Models: []core.ModelOption{{Name: "vendor/custom-model", Alias: "custom"}},
	}})
	a.SetActiveProvider("custom")
	for _, input := range []string{"vendor/custom-model", "CUSTOM"} {
		got, err := validator.ValidateModel(context.Background(), input)
		if err != nil || got != "vendor/custom-model" {
			t.Fatalf("provider model %q = %q, %v", input, got, err)
		}
	}
}

func TestValidateModel_UsesEffectiveCodexHome(t *testing.T) {
	for _, source := range []string{"option", "config env", "provider env", "session env"} {
		t.Run(source, func(t *testing.T) {
			isolateModelValidation(t)
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_catalog_json = 'catalog.json'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			catalog := `{"models":[{"slug":"custom-home-model","visibility":"list","supported_in_api":true}]}`
			if err := os.WriteFile(filepath.Join(home, "catalog.json"), []byte(catalog), 0o600); err != nil {
				t.Fatal(err)
			}
			a := &Agent{activeIdx: -1}
			switch source {
			case "option":
				a.codexHome = home
			case "config env":
				a.configEnv = []string{"CODEX_HOME=" + home}
			case "provider env":
				a.configEnv = []string{"CODEX_HOME=" + t.TempDir()}
				a.providers = []core.ProviderConfig{{Env: map[string]string{"CODEX_HOME": home}}}
				a.activeIdx = 0
			case "session env":
				a.configEnv = []string{"CODEX_HOME=" + t.TempDir()}
				a.sessionEnv = []string{"CODEX_HOME=" + home}
			}
			got, err := a.ValidateModel(context.Background(), "custom-home-model")
			if err != nil || got != "custom-home-model" {
				t.Fatalf("effective home model = %q, %v", got, err)
			}
		})
	}
}
