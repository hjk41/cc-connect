package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// This subprocess implements only metadata RPCs. Any attempt to create a
// thread, start a turn, or use a different workspace fails the test.
func TestSkillsMetadataProcess(t *testing.T) {
	if os.Getenv("CC_SKILLS_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for _, method := range []string{"initialize", "initialized", "skills/list"} {
		if !scanner.Scan() {
			os.Exit(2)
		}
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.Method != method {
			os.Exit(3)
		}
		if method == "initialized" {
			continue
		}
		if method == "initialize" {
			fmt.Printf("{\"id\":%d,\"result\":{}}\n", req.ID)
			continue
		}
		cwd, _ := os.Getwd()
		cwds, _ := req.Params["cwds"].([]any)
		var requestedCwd string
		if len(cwds) == 1 {
			requestedCwd, _ = cwds[0].(string)
		}
		if !sameCodexPath(requestedCwd, cwd) || req.Params["forceReload"] != true || os.Getenv("CODEX_HOME") != os.Getenv("CC_EXPECT_CODEX_HOME") {
			os.Exit(4)
		}
		if os.Getenv("CC_SKILLS_HANG") == "1" {
			time.Sleep(time.Minute)
		}
		fmt.Printf("{\"id\":%d,\"result\":%s}\n", req.ID, os.Getenv("CC_SKILLS_RESPONSE"))
	}
	// Remain alive so the caller must clean up the metadata process.
	scanner.Scan()
	os.Exit(0)
}

func skillsMetadataTestAgent(t *testing.T, workDir, codexHome string, response any) *Agent {
	t.Helper()
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &Agent{cmd: bin, cliExtraArgs: []string{"-test.run=^TestSkillsMetadataProcess$", "--"}, workDir: workDir, codexHome: codexHome,
		configEnv: []string{"CC_SKILLS_HELPER=1", "CC_EXPECT_CODEX_HOME=" + codexHome, "CC_SKILLS_RESPONSE=" + string(data)}}
}

func TestListSkills_ExcludesClaudeDisabledAndCachedSkills(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "workspace")
	codexHome := filepath.Join(tmp, "codex")
	setTestHome(t, tmp)
	t.Setenv("CODEX_HOME", filepath.Join(tmp, "wrong-home"))
	write := func(root, name string) string {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: Fixture\n---\nInstructions for "+name), 0644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	project := write(filepath.Join(workDir, ".agents", "skills"), "onboarding")
	system := write(filepath.Join(codexHome, "skills", ".system"), "system")
	pluginRoot := filepath.Join(codexHome, "plugins", "cache", "market", "plugin", "v1", "skills")
	plugin := write(pluginRoot, "enabled")
	disabled := write(pluginRoot, "disabled-sibling")
	write(filepath.Join(workDir, ".claude", "skills"), "claude-only")
	write(filepath.Join(codexHome, "superpowers", "skills"), "unlinked-superpower")
	write(filepath.Join(codexHome, "plugins", "cache", "market", "unused", "v0", "skills"), "cached-only")

	response := map[string]any{"data": []any{map[string]any{
		"cwd": workDir, "errors": []any{}, "skills": []any{
			map[string]any{"name": "onboarding", "path": project, "enabled": true, "description": "Project skill"},
			map[string]any{"name": "system", "path": system, "enabled": true},
			map[string]any{"name": "plugin:enabled", "path": plugin, "enabled": true},
			map[string]any{"name": "plugin:disabled", "path": disabled, "enabled": false},
		},
	}}}
	a := skillsMetadataTestAgent(t, workDir, codexHome, response)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	skills, err := a.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
		if !strings.HasPrefix(s.Prompt, "Instructions for ") {
			t.Fatalf("skill instructions not loaded: %+v", s)
		}
	}
	if want := []string{"onboarding", "system", "plugin:enabled"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("skills=%v, want %v", names, want)
	}
	// Both text /skills and card navigation use this interface in core.
	var _ core.SkillCatalogProvider = a

	a.configEnv = append(a.configEnv, "CC_SKILLS_HANG=1")
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := a.ListSkills(ctx); err == nil {
		t.Fatal("hung metadata process must fail")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("metadata process did not respect cancellation")
	}
}

func TestListSkills_AcceptsSymlinkedWorkspace(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real-workspace")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realDir, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "workspace-alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	skill := filepath.Join(realDir, "SKILL.md")
	if err := os.WriteFile(skill, []byte("---\nname: linked\ndescription: Fixture\n---\nLinked workspace instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := map[string]any{"data": []any{map[string]any{
		"cwd": realDir, "skills": []any{map[string]any{"name": "linked", "path": skill, "enabled": true}},
	}}}
	a := skillsMetadataTestAgent(t, alias, filepath.Join(root, "codex"), response)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	skills, err := a.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "linked" || skills[0].Prompt != "Linked workspace instructions" {
		t.Fatalf("skills = %#v, want linked workspace skill", skills)
	}
}

func TestSameCodexPath_RecognizesDirectoryAliases(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	otherDir := filepath.Join(root, "other")
	for _, dir := range []string{realDir, otherDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, tc := range []struct {
		name string
		a, b string
		want bool
	}{
		{"alias to real", alias, realDir, true},
		{"real to alias", realDir, alias, true},
		{"different directories", alias, otherDir, false},
		{"empty path", "", realDir, false},
		{"identical missing path", filepath.Join(root, "missing"), filepath.Join(root, "missing"), true},
		{"different missing paths", filepath.Join(root, "missing"), filepath.Join(root, "absent"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameCodexPath(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameCodexPath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestListSkills_CatalogErrorsAndEmptyAreAuthoritative(t *testing.T) {
	workDir := t.TempDir()
	for _, tc := range []struct {
		name, payload string
		wantError     bool
	}{
		{"empty", `{"data":[{"cwd":%q,"skills":[]}]}`, false},
		{"missing workspace", `{"data":[],"ignored":%q}`, true},
		{"discovery error", `{"data":[{"cwd":%q,"errors":[{"message":"invalid skill"}]}]}`, true},
		{"relative file", `{"data":[{"cwd":%q,"skills":[{"enabled":true,"name":"bad","path":"relative/SKILL.md"}]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r codexSkillsResponse
			if err := json.Unmarshal([]byte(fmt.Sprintf(tc.payload, workDir)), &r); err != nil {
				t.Fatal(err)
			}
			skills, err := r.load(workDir)
			if (err != nil) != tc.wantError || len(skills) != 0 {
				t.Fatalf("skills=%v err=%v", skills, err)
			}
		})
	}
}
