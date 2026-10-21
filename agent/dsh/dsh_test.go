package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"github.com/klauspost/compress/zstd"
)

func TestAppliesReasoningOnResume(t *testing.T) {
	agent := testAgent(t, nil)
	if !agent.AppliesReasoningOnResume() {
		t.Fatal("dsh reasoning effort should apply to the current conversation")
	}
}

func testAgent(t *testing.T, opts map[string]any) *Agent {
	t.Helper()
	if opts == nil {
		opts = map[string]any{}
	}
	if _, ok := opts["cmd"]; !ok {
		opts["cmd"] = "go"
	}
	if _, ok := opts["work_dir"]; !ok {
		opts["work_dir"] = t.TempDir()
	}
	raw, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent, ok := raw.(*Agent)
	if !ok {
		t.Fatalf("New returned %T", raw)
	}
	return agent
}

func TestNewDefaultsAndModeEnv(t *testing.T) {
	home := t.TempDir()
	agent := testAgent(t, map[string]any{
		"dsh_home":         home,
		"mode":             "bypassPermissions",
		"reasoning_effort": "medium",
		"env":              map[string]string{"DSH_AGENTS_HOME": filepath.Join(home, "agents")},
	})
	if agent.Name() != "dsh" || agent.CLIBinaryName() != "dsh" || agent.CLIDisplayName() != "DeepSeek Harness" {
		t.Fatalf("identity = %s %s %s", agent.Name(), agent.CLIBinaryName(), agent.CLIDisplayName())
	}
	if agent.HasSystemPromptSupport() {
		t.Fatal("dsh must not advertise a system-prompt channel")
	}
	if _, ok := any(agent).(core.ContextCompressor); ok {
		t.Fatal("dsh must not advertise ContextCompressor")
	}
	if agent.GetModel() != "deepseek-flash" {
		t.Fatalf("model = %q", agent.GetModel())
	}
	if agent.GetMode() != modeYolo {
		t.Fatalf("mode = %q", agent.GetMode())
	}
	if agent.GetReasoningEffort() != "high" {
		t.Fatalf("effort = %q", agent.GetReasoningEffort())
	}
	env := strings.Join(agent.spawnEnv(), "\n")
	if !strings.Contains(env, "DSH_PERMISSION_MODE="+envFullAccess) {
		t.Fatalf("env = %s", env)
	}
	if !strings.Contains(env, "DSH_HOME="+home) {
		t.Fatalf("missing DSH_HOME in %s", env)
	}
	agent.SetMode("read-only")
	if agent.GetMode() != modePlan {
		t.Fatalf("plan mode = %q", agent.GetMode())
	}
	if !strings.Contains(strings.Join(agent.spawnEnv(), "\n"), "DSH_PERMISSION_MODE="+envReadOnly) {
		t.Fatal("read-only was not mapped")
	}
	agent.SetMode("nope")
	if agent.GetMode() != modePlan {
		t.Fatal("unknown mode replaced the current mode")
	}
	if len(agent.PermissionModes()) != 3 {
		t.Fatalf("modes = %#v", agent.PermissionModes())
	}
}

func TestEffortAliases(t *testing.T) {
	agent := testAgent(t, nil)
	cases := map[string]string{
		"off": "off", "low": "low", "medium": "high", "high": "high",
		"xhigh": "max", "max": "max", "minimal": "off", "none": "off",
	}
	for in, want := range cases {
		agent.SetReasoningEffort(in)
		if got := agent.GetReasoningEffort(); got != want {
			t.Errorf("SetReasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
	agent.SetReasoningEffort("high")
	agent.SetReasoningEffort("banana")
	if agent.GetReasoningEffort() != "high" {
		t.Fatalf("unknown effort changed value to %q", agent.GetReasoningEffort())
	}
}

func TestModelMatchingOpaqueNameAndInnerID(t *testing.T) {
	agent := testAgent(t, nil)
	opaqueFlash := `["deepseek-official","deepseek-flash"]`
	opaquePro := `["deepseek-official","deepseek-v4-pro"]`
	agent.RememberModelOptions("", []core.ModelOption{
		{Name: opaqueFlash, Desc: "DeepSeek-V4.1-Flash"},
		{Name: opaquePro, Desc: "DeepSeek-V4-Pro"},
	})
	models := agent.AvailableModels(context.Background())
	if len(models) != 2 || models[0].Name != "deepseek-flash" || models[0].Desc != "DeepSeek-V4.1-Flash" {
		t.Fatalf("presented = %#v", models)
	}
	ctx := context.Background()
	for _, input := range []string{opaqueFlash, "deepseek-flash", "DeepSeek-V4.1-Flash"} {
		got, err := agent.ValidateModel(ctx, input)
		if err != nil || got != "deepseek-flash" {
			t.Fatalf("ValidateModel(%q) = %q, %v", input, got, err)
		}
	}
	if _, err := agent.ValidateModel(ctx, "not-a-model"); err == nil {
		t.Fatal("unknown model was accepted")
	}
}

func TestProviderEnvAndBalance(t *testing.T) {
	agent := testAgent(t, map[string]any{
		"env": map[string]string{"DEEPSEEK_API_KEY": "from-config"},
	})
	agent.SetProviders([]core.ProviderConfig{{
		Name:    "official",
		APIKey:  "from-provider",
		BaseURL: "https://example.test/anthropic",
		Env:     map[string]string{"DEEPSEEK_API_KEY": "from-env-map", "CUSTOM_FLAG": "1"},
	}})
	if !agent.SetActiveProvider("official") {
		t.Fatal("provider was not selected")
	}
	env := agent.spawnEnv()
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "DEEPSEEK_API_KEY=from-provider") {
		t.Fatalf("api key env = %s", joined)
	}
	if strings.Contains(joined, "from-env-map") || strings.Contains(joined, "from-config") {
		t.Fatal("provider api_key did not win")
	}
	if !strings.Contains(joined, "DEEPSEEK_BASE_URL=https://example.test/anthropic") {
		t.Fatal("base url was not forwarded")
	}
	if !strings.Contains(joined, "CUSTOM_FLAG=1") {
		t.Fatal("provider env was dropped")
	}
	if agent.apiKey() != "from-provider" {
		t.Fatalf("apiKey = %q", agent.apiKey())
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer from-provider" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"is_available": true,
			"balance_infos": []map[string]string{{
				"currency":          "CNY",
				"total_balance":     "12.50",
				"granted_balance":   "0.50",
				"topped_up_balance": "12.00",
			}},
		})
	}))
	defer srv.Close()
	agent.mu.Lock()
	agent.balanceURL = srv.URL
	agent.mu.Unlock()
	report, err := agent.GetUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Provider != "deepseek" || report.AccountID != "" || report.Plan != "DeepSeek" || report.Credits == nil || report.Credits.Balance != "CNY 12.50" || !report.Credits.HasCredits {
		t.Fatalf("report = %#v credits=%#v", report, report.Credits)
	}
}

func TestParseBalanceUnavailable(t *testing.T) {
	report, err := parseBalance([]byte(`{"is_available":false,"balance_infos":[{"currency":"USD","total_balance":"0"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if report.Plan != "DeepSeek unavailable" || report.Credits.HasCredits {
		t.Fatalf("report = %#v", report)
	}
}

func TestHistoryFixture(t *testing.T) {
	path := filepath.Join("testdata", "session.v4.jsonl.zstd")
	meta, err := parseSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != "11111111-1111-1111-1111-111111111111" || meta.Cwd != "/work/demo" {
		t.Fatalf("header = %+v", meta)
	}
	if meta.Title != "Pong check" {
		t.Fatalf("title = %q", meta.Title)
	}
	if len(meta.History) != 2 || meta.History[0].Role != "user" || meta.History[0].Content != "reply with the word pong" {
		t.Fatalf("history = %#v", meta.History)
	}
	if meta.History[1].Role != "assistant" || meta.History[1].Content != "pong" {
		t.Fatalf("assistant = %#v", meta.History[1])
	}
	if !meta.CreatedAt.Equal(time.UnixMilli(1700000000000)) {
		t.Fatalf("created = %s", meta.CreatedAt)
	}
}

func TestListFiltersWorkDirAndDeleteIsSafe(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	other := t.TempDir()
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	if _, err := os.Stat("/usr/bin/true"); err == nil {
		agent.ProbeCommand = "/usr/bin/true"
		agent.ProbeArgs = nil
	}
	keepID := "22222222-2222-2222-2222-222222222222"
	dropID := "33333333-3333-3333-3333-333333333333"
	writeSession(t, home, keepID, work, "keep me")
	writeSession(t, home, dropID, other, "other project")

	listed, err := agent.scanSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != keepID || listed[0].Title != "keep me" {
		t.Fatalf("listed = %#v", listed)
	}
	if !agent.ValidateSessionID(context.Background(), keepID) {
		t.Fatal("kept id should validate")
	}
	if agent.ValidateSessionID(context.Background(), dropID) {
		t.Fatal("other work dir must not validate")
	}
	hist, err := agent.GetSessionHistory(context.Background(), keepID, 0)
	if err != nil || len(hist) != 1 || hist[0].Content != "keep me" {
		t.Fatalf("history = %#v err=%v", hist, err)
	}
	if title := agent.GetSessionTitle(keepID); title != "keep me" {
		t.Fatalf("title = %q", title)
	}
	if agent.ValidateSessionID(context.Background(), "") || agent.ValidateSessionID(context.Background(), "../"+keepID) {
		t.Fatal("empty and traversal ids must be rejected")
	}
	agent.excludeProbe(work, keepID)
	again, err := agent.scanSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("probe session leaked into the list: %#v", again)
	}
}

func TestFindSessionDirSkipsNonMatchingDirectories(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	target := "55555555-5555-5555-5555-555555555555"
	decoyName := "66666666-6666-6666-6666-666666666666"
	real := writeSession(t, home, target, work, "real prompt")

	// Header id is the target, but the directory name is not. Reading this
	// log would make the target look like a duplicate match.
	decoy := filepath.Join(home, "sessions", "other-slug", decoyName)
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(decoy, "session.v4.jsonl.zstd"), sessionLog(target, work, "should not be parsed"))

	bad := filepath.Join(home, "sessions", "other-slug", "not-a-session")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "session.v4.jsonl.zstd"), []byte("not-zstd"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, meta, err := agent.findSessionDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if dir != real || meta.ID != target || meta.Title != "real prompt" {
		t.Fatalf("found = %s meta=%+v", dir, meta)
	}
	if _, _, err := agent.findSessionDir(decoyName); err == nil || !errors.Is(err, errSessionNotFound) {
		t.Fatalf("decoy directory with a foreign header = %v", err)
	}
}

func TestValidateSessionIDFromDisk(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	other := t.TempDir()
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	keepID := "22222222-2222-2222-2222-222222222222"
	otherID := "33333333-3333-3333-3333-333333333333"
	missingID := "77777777-7777-7777-7777-777777777777"
	writeSession(t, home, keepID, work, "keep")
	writeSession(t, home, otherID, other, "other")

	if !agent.ValidateSessionID(context.Background(), keepID) {
		t.Fatal("matching session should validate")
	}
	if agent.ValidateSessionID(context.Background(), otherID) {
		t.Fatal("other work dir should not validate")
	}
	if agent.ValidateSessionID(context.Background(), missingID) {
		t.Fatal("missing session should not validate")
	}

	absent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": t.TempDir()})
	if absent.ValidateSessionID(context.Background(), keepID) {
		t.Fatal("absent sessions directory is not found")
	}

	brokenHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(brokenHome, "sessions"), []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := testAgent(t, map[string]any{"work_dir": work, "dsh_home": brokenHome})
	if !broken.ValidateSessionID(context.Background(), keepID) {
		t.Fatal("disk read error should keep the id")
	}
}

func TestDeleteRemovesOnlyMatchingDir(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	keepID := "22222222-2222-2222-2222-222222222222"
	dropID := "33333333-3333-3333-3333-333333333333"
	keepDir := writeSession(t, home, keepID, work, "keep")
	dropDir := writeSession(t, home, dropID, work, "drop")

	if err := agent.DeleteSession(context.Background(), "not a safe/id"); err == nil {
		t.Fatal("slash id was accepted")
	}
	if err := agent.DeleteSession(context.Background(), dropID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dropDir); !os.IsNotExist(err) {
		t.Fatalf("drop dir still exists: %v", err)
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Fatalf("sibling removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions")); err != nil {
		t.Fatal("sessions root was removed")
	}
	if _, err := os.Stat(filepath.Dir(dropDir)); err != nil {
		t.Fatal("cwd slug directory was removed")
	}

	mismatch := "44444444-4444-4444-4444-444444444444"
	dir := filepath.Join(home, "sessions", "slug", mismatch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(dir, "session.v4.jsonl.zstd"), sessionLog(keepID, work, "nope"))
	if err := agent.DeleteSession(context.Background(), mismatch); err == nil {
		t.Fatal("header mismatch was deleted")
	}
}

func writeSession(t *testing.T, home, id, cwd, prompt string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "slug-"+id[:8], id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(dir, "session.v4.jsonl.zstd"), sessionLog(id, cwd, prompt))
	return dir
}

func writeLog(t *testing.T, path, body string) {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if err := os.WriteFile(path, enc.EncodeAll([]byte(body), nil), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sessionLog(id, cwd, prompt string) string {
	header := map[string]any{
		"type": "session", "version": 4, "id": id, "createdAt": int64(1700000000000), "cwd": cwd,
	}
	user := map[string]any{
		"type": "user/message", "time": int64(1700000001000),
		"data": map[string]any{
			"role": "user", "source": map[string]any{"kind": "user"},
			"content": []map[string]string{{"type": "text", "text": prompt}},
		},
	}
	title := map[string]any{
		"type": "session/title", "time": int64(1700000002000),
		"data": map[string]any{"title": prompt, "source": map[string]any{"kind": "fallback"}},
	}
	var b strings.Builder
	for _, item := range []any{header, user, title} {
		raw, err := json.Marshal(item)
		if err != nil {
			panic(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestFindSessionDir_FileRootIsReadError(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "sessions"), []byte("broken root"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent := testAgent(t, map[string]any{"work_dir": t.TempDir(), "dsh_home": home})
	_, _, err := agent.findSessionDir("22222222-2222-2222-2222-222222222222")
	if err == nil || errors.Is(err, errSessionNotFound) {
		t.Fatalf("file root must preserve a read error, got %v", err)
	}
}
