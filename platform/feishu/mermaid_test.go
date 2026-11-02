package feishu

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFindMermaidFences(t *testing.T) {
	tests := []struct {
		name       string
		md         string
		wantCount  int
		wantSource []string
	}{
		{
			name:       "backtick lowercase",
			md:         "before\n```mermaid\ngraph TD\nA-->B\n```\nafter",
			wantCount:  1,
			wantSource: []string{"graph TD\nA-->B"},
		},
		{
			name:       "tilde uppercase",
			md:         "~~~MERMAID\nsequenceDiagram\nA->>B: hi\n~~~",
			wantCount:  1,
			wantSource: []string{"sequenceDiagram\nA->>B: hi"},
		},
		{
			name:       "long closing marker",
			md:         "````mermaid\nflowchart LR\nA-->B\n`````\n",
			wantCount:  1,
			wantSource: []string{"flowchart LR\nA-->B"},
		},
		{
			name:      "unclosed fence",
			md:        "```mermaid\ngraph TD\nA-->B\n",
			wantCount: 0,
		},
		{
			name:      "short closing marker",
			md:        "````mermaid\ngraph TD\n```\nstill code\n",
			wantCount: 0,
		},
		{
			name:      "nested inside code block",
			md:        "```text\n```mermaid\ngraph TD\n```\n```\n",
			wantCount: 0,
		},
		{
			name:      "mermaid text inside longer code block",
			md:        "````text\n~~~mermaid\ngraph TD\n~~~\n````\n",
			wantCount: 0,
		},
		{
			name:      "non mermaid label",
			md:        "```js\nconsole.log(1)\n```\n",
			wantCount: 0,
		},
		{
			name:      "multiple preserve order",
			md:        "```mermaid\nfirst\n```\nmiddle\n~~~mermaid\nsecond\n~~~\n",
			wantCount: 2,
			wantSource: []string{
				"first",
				"second",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fences := findMermaidFences(tt.md)
			if len(fences) != tt.wantCount {
				t.Fatalf("fence count = %d, want %d (%+v)", len(fences), tt.wantCount, fences)
			}
			for i, want := range tt.wantSource {
				if fences[i].source != want {
					t.Fatalf("fence[%d].source = %q, want %q", i, fences[i].source, want)
				}
				if tt.md[fences[i].start:fences[i].end] == "" {
					t.Fatalf("fence[%d] has empty offsets", i)
				}
			}
		})
	}
}

func newMermaidTestPlatform(render func(context.Context, string) (string, error)) *Platform {
	p := &Platform{platformName: "feishu", mermaidRenderer: "test", mermaidTimeout: time.Second}
	p.mermaidResolver = newMermaidResolver("test-settings", time.Second, render)
	return p
}

func TestResolveRichCardMarkdownReplacesCompleteMermaid(t *testing.T) {
	var seen []string
	p := newMermaidTestPlatform(func(_ context.Context, source string) (string, error) {
		seen = append(seen, source)
		return "img_v3_mermaid", nil
	})
	input := "before\n```mermaid\ngraph TD\nA-->B\n```\nafter"
	got := p.ResolveRichCardMarkdown(context.Background(), input, true)
	if !strings.Contains(got, "![Mermaid](img_v3_mermaid)") {
		t.Fatalf("resolved markdown missing image key: %q", got)
	}
	if !strings.HasPrefix(got, "before\n") || !strings.HasSuffix(got, "\nafter") {
		t.Fatalf("surrounding text was not preserved: %q", got)
	}
	if len(seen) != 1 || seen[0] != "graph TD\nA-->B" {
		t.Fatalf("rendered sources = %q", seen)
	}
}

func TestResolveRichCardMarkdown_MermaidFallbackAndCodeOnly(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		p := &Platform{platformName: "feishu"}
		input := "```mermaid\ngraph TD\n```"
		if got := p.ResolveRichCardMarkdown(context.Background(), input, true); got != input {
			t.Fatalf("disabled resolver changed source: %q", got)
		}
	})
	t.Run("missing renderer", func(t *testing.T) {
		p := newMermaidTestPlatform(func(context.Context, string) (string, error) {
			return "", errMermaidMissing
		})
		input := "```mermaid\ngraph TD\n```"
		if got := p.ResolveRichCardMarkdown(context.Background(), input, true); got != input {
			t.Fatalf("failed renderer changed source: %q", got)
		}
	})
	t.Run("incomplete fence", func(t *testing.T) {
		calls := 0
		p := newMermaidTestPlatform(func(context.Context, string) (string, error) {
			calls++
			return "img", nil
		})
		input := "```mermaid\ngraph TD\n"
		if got := p.ResolveRichCardMarkdown(context.Background(), input, true); got != input {
			t.Fatalf("incomplete fence changed: %q", got)
		}
		if calls != 0 {
			t.Fatalf("render calls = %d, want 0", calls)
		}
	})
	t.Run("ordinary text", func(t *testing.T) {
		p := newMermaidTestPlatform(func(context.Context, string) (string, error) {
			t.Fatal("renderer called for ordinary text")
			return "", nil
		})
		input := "ordinary **bold** text"
		if got := p.ResolveRichCardMarkdown(context.Background(), input, true); got != input {
			t.Fatalf("ordinary text changed: %q", got)
		}
	})
}

func TestResolveRichCardMarkdown_MultipleDiagramsAndLimit(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	p := newMermaidTestPlatform(func(_ context.Context, source string) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return "img_" + source, nil
	})
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("```mermaid\ndiagram")
		b.WriteByte(byte('0' + i))
		b.WriteString("\n```\n")
	}
	got := p.ResolveRichCardMarkdown(context.Background(), b.String(), true)
	for i := 0; i < 4; i++ {
		if !strings.Contains(got, "![Mermaid](img_diagram"+string(rune('0'+i))+")") {
			t.Fatalf("diagram %d missing from %q", i, got)
		}
	}
	if !strings.Contains(got, "diagram4") {
		t.Fatalf("fifth diagram source should remain: %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 4 {
		t.Fatalf("render calls = %d, want 4", calls)
	}
}

func TestResolveRichCardMarkdown_StreamingReturnsPromptlyAndDeduplicates(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var (
		mu    sync.Mutex
		calls int
	)
	p := newMermaidTestPlatform(func(_ context.Context, _ string) (string, error) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			close(started)
		}
		<-release
		return "img_v3_dedup", nil
	})
	input := "```mermaid\ngraph TD\nA-->B\n```"

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			got := p.ResolveRichCardMarkdown(context.Background(), input, false)
			if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
				t.Errorf("streaming resolve waited %s", elapsed)
			}
			if got != input {
				t.Errorf("streaming resolve should retain source, got %q", got)
			}
		}()
	}
	wg.Wait()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("renderer did not start")
	}
	mu.Lock()
	if calls != 1 {
		t.Fatalf("render calls while first blocked = %d, want 1", calls)
	}
	mu.Unlock()
	close(release)

	final := p.ResolveRichCardMarkdown(context.Background(), input, true)
	if !strings.Contains(final, "![Mermaid](img_v3_dedup)") {
		t.Fatalf("final resolve missing image: %q", final)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("render calls after final = %d, want deduplicated 1", calls)
	}
}

func TestResolveRichCardMarkdown_FinalWaitIsBounded(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	p := newMermaidTestPlatform(func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-release
		return "img", nil
	})
	p.mermaidResolver.finalWait = 120 * time.Millisecond

	input := "```mermaid\ngraph TD\n```"
	start := time.Now()
	got := p.ResolveRichCardMarkdown(context.Background(), input, true)
	elapsed := time.Since(start)
	if elapsed > 600*time.Millisecond {
		t.Fatalf("final resolve waited %s, want bounded near 120ms", elapsed)
	}
	if got != input {
		t.Fatalf("unresolved final should retain source, got %q", got)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("renderer did not start")
	}
}

func TestResolveRichCardMarkdown_QueueIsBounded(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	var once sync.Once
	p := newMermaidTestPlatform(func(context.Context, string) (string, error) {
		once.Do(func() { close(started) })
		<-release
		return "img", nil
	})

	for i := 0; i < mermaidMaxPendingJobs+1; i++ {
		input := "```mermaid\ndiagram" + string(rune('0'+i)) + "\n```"
		start := time.Now()
		got := p.ResolveRichCardMarkdown(context.Background(), input, false)
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Fatalf("resolve %d waited %s, want prompt fallback", i, elapsed)
		}
		if got != input {
			t.Fatalf("unexpected replacement before render: %q", got)
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("renderer did not start")
	}
	p.mermaidResolver.mu.Lock()
	pending := len(p.mermaidResolver.pending)
	p.mermaidResolver.mu.Unlock()
	if pending > mermaidMaxPendingJobs {
		t.Fatalf("pending jobs = %d, want <= %d", pending, mermaidMaxPendingJobs)
	}
}

func TestResolveRichCardMarkdown_FailedSourceNotRetried(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	p := newMermaidTestPlatform(func(context.Context, string) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return "", errMermaidRenderFailed
	})
	input := "```mermaid\ninvalid syntax\n```"
	for i := 0; i < 3; i++ {
		if got := p.ResolveRichCardMarkdown(context.Background(), input, true); got != input {
			t.Fatalf("failed diagram changed on call %d: %q", i, got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("render calls = %d, want failed source cached after one attempt", calls)
	}
}

func TestResolveRichCardMarkdown_StopCancelsRender(t *testing.T) {
	started := make(chan struct{})
	p := newMermaidTestPlatform(func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	input := "```mermaid\ngraph TD\n```"
	_ = p.ResolveRichCardMarkdown(context.Background(), input, false)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("renderer did not start")
	}
	start := time.Now()
	p.mermaidResolver.Stop()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Stop took %s, want prompt cancellation", elapsed)
	}
	// Stop is idempotent and the failed outcome is cached.
	p.mermaidResolver.Stop()
	if got := p.ResolveRichCardMarkdown(context.Background(), input, true); got != input {
		t.Fatalf("stopped resolver should retain source, got %q", got)
	}
}

func TestMermaidCacheIsBounded(t *testing.T) {
	r := newMermaidResolver("test", time.Second, nil)
	for i := 0; i < mermaidCacheMaxEntries+10; i++ {
		r.mu.Lock()
		r.putCacheLocked(string(rune(i)), "img", false)
		r.mu.Unlock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) != mermaidCacheMaxEntries {
		t.Fatalf("cache size = %d, want %d", len(r.cache), mermaidCacheMaxEntries)
	}
	if _, ok := r.cache[string(rune(0))]; ok {
		t.Fatal("oldest cache entry should have been evicted")
	}
}

func validTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestValidateMermaidPNG(t *testing.T) {
	if err := validateMermaidPNG(validTestPNG(t, 2, 2)); err != nil {
		t.Fatalf("valid PNG rejected: %v", err)
	}
	if err := validateMermaidPNG([]byte("not a png")); err == nil {
		t.Fatal("malformed PNG accepted")
	}
	if err := validateMermaidPNG(validTestPNG(t, mermaidMaxDimension+1, 2)); err == nil {
		t.Fatal("oversized PNG dimensions accepted")
	}
}

func TestMermaidSafeEnvExcludesSecretsAndProxies(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("HTTP_PROXY", "http://proxy")
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("CC_MERMAID_CHROME", "/chrome")
	env := mermaidSafeEnv()
	joined := strings.Join(env, "\n")
	for _, banned := range []string{"AWS_SECRET_ACCESS_KEY", "HTTP_PROXY", "GITHUB_TOKEN"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("safe env leaked %s", banned)
		}
	}
	if !strings.Contains(joined, "CC_MERMAID_CHROME=/chrome") {
		t.Fatal("safe env should forward CC_MERMAID_CHROME")
	}
}

func TestRenderMermaidPNGUsesArgvAndCleansTempDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-only")
	}
	pngData := validTestPNG(t, 2, 2)
	fixtureDir := t.TempDir()
	pngPath := filepath.Join(fixtureDir, "fixture.png")
	if err := os.WriteFile(pngPath, pngData, 0o600); err != nil {
		t.Fatal(err)
	}
	argLog := filepath.Join(fixtureDir, "args.log")
	script := filepath.Join(fixtureDir, "renderer.sh")
	scriptBody := "#!/bin/sh\n" +
		"input=\"\"; output=\"\";\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in --input) input=\"$2\"; shift 2;; --output) output=\"$2\"; shift 2;; *) exit 2;; esac; done\n" +
		"printf '%s\\n%s\\n' \"$input\" \"$output\" > " + argLog + "\n" +
		"cp " + pngPath + " \"$output\"\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatal(err)
	}

	p := &Platform{
		platformName:       "feishu",
		mermaidRenderer:    script,
		mermaidTimeout:     5 * time.Second,
		mermaidTempDirBase: fixtureDir,
		mermaidUploadFunc: func(_ context.Context, data []byte) (string, error) {
			if !bytes.Equal(data, pngData) {
				t.Fatal("upload received wrong PNG bytes")
			}
			return "img_v3_script", nil
		},
	}
	key, err := p.renderMermaidPNG(context.Background(), "graph TD\nA-->B")
	if err != nil {
		t.Fatalf("renderMermaidPNG() error = %v", err)
	}
	if key != "img_v3_script" {
		t.Fatalf("key = %q", key)
	}
	args, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 2 {
		t.Fatalf("args log = %q", args)
	}
	if !strings.HasSuffix(lines[0], "diagram.mmd") || !strings.HasSuffix(lines[1], "diagram.png") {
		t.Fatalf("unexpected argv paths: %q", lines)
	}
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "cc-connect-mermaid-") {
			t.Fatalf("temporary render directory leaked: %s", entry.Name())
		}
	}
}

func TestRenderMermaidPNGInvalidOutputCleansTempDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-only")
	}
	fixtureDir := t.TempDir()
	script := filepath.Join(fixtureDir, "renderer.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := &Platform{
		platformName:       "feishu",
		mermaidRenderer:    script,
		mermaidTimeout:     5 * time.Second,
		mermaidTempDirBase: fixtureDir,
	}
	_, err := p.renderMermaidPNG(context.Background(), "graph TD")
	if !errors.Is(err, errMermaidInvalidOutput) {
		t.Fatalf("error = %v, want invalid output", err)
	}
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "cc-connect-mermaid-") {
			t.Fatalf("temporary render directory leaked: %s", entry.Name())
		}
	}
}

func TestRenderMermaidPNGTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-only")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	fixtureDir := t.TempDir()
	script := filepath.Join(fixtureDir, "renderer.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := &Platform{
		platformName:       "feishu",
		mermaidRenderer:    script,
		mermaidTimeout:     100 * time.Millisecond,
		mermaidTempDirBase: fixtureDir,
	}
	start := time.Now()
	_, err := p.renderMermaidPNG(context.Background(), "graph TD")
	if !errors.Is(err, errMermaidTimeout) {
		t.Fatalf("error = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestFencedMermaidSourceProtectedFromImageAndURLSanitizers(t *testing.T) {
	inputs := []string{
		"```mermaid\nflowchart TD\n    A[\"![x](https://example.com/a.png)\"]\n```",
		"~~~mermaid\nflowchart TD\n    A[\"![x](https://example.com/a.png)\"]\n~~~",
		"```mermaid\nflowchart TD\n    A[\"![x](https://example.com/a.png)\"]\n",
		"~~~mermaid\nflowchart TD\n    A[\"![x](https://example.com/a.png)\"]\n",
	}
	for _, input := range inputs {
		t.Run(strings.Split(input, "\n")[0], func(t *testing.T) {
			p := newMermaidTestPlatform(func(context.Context, string) (string, error) {
				return "", errMermaidRenderFailed
			})
			var uploadCalls int
			p.richCardImageUploadFunc = func(context.Context, string) (string, error) {
				uploadCalls++
				return "img_should_not_happen", nil
			}
			resolved := p.ResolveRichCardMarkdown(context.Background(), input, true)
			if resolved != input {
				t.Fatalf("failed mermaid resolution changed source:\n got %q\nwant %q", resolved, input)
			}
			if uploadCalls != 0 {
				t.Fatalf("remote uploader called %d times for fenced source", uploadCalls)
			}
			sanitized := sanitizeCardMarkdownForCard(resolved)
			if !strings.Contains(sanitized, `![x](https://example.com/a.png)`) {
				t.Fatalf("card sanitation deleted fenced image syntax: %q", sanitized)
			}
		})
	}
}

func TestSanitizeMarkdownURLsProtectsAllFences(t *testing.T) {
	for _, fence := range []struct {
		name  string
		open  string
		close string
	}{
		{name: "backtick complete", open: "```", close: "```"},
		{name: "tilde complete", open: "~~~", close: "~~~"},
		{name: "backtick incomplete", open: "```", close: ""},
		{name: "tilde incomplete", open: "~~~", close: ""},
	} {
		t.Run(fence.name, func(t *testing.T) {
			inside := "[bad](javascript:alert(1))"
			input := fence.open + "\n" + inside + "\n" + fence.close
			if fence.close == "" {
				input = fence.open + "\n" + inside + "\n"
			}
			got := sanitizeMarkdownURLs(input)
			if got != input {
				t.Fatalf("sanitizeMarkdownURLs changed fenced source: %q -> %q", input, got)
			}
			outside := "outside " + inside
			gotOutside := sanitizeMarkdownURLs(outside)
			if strings.Contains(gotOutside, "](javascript:") {
				t.Fatalf("outside link was not sanitized: %q", gotOutside)
			}
		})
	}
}

func TestStripInvalidFeishuCardImagesProtectsAllFences(t *testing.T) {
	for _, input := range []string{
		"```\n![x](https://example.com/a.png)\n```",
		"~~~\n![x](https://example.com/a.png)\n~~~",
		"```\n![x](https://example.com/a.png)\n",
		"~~~\n![x](https://example.com/a.png)\n",
	} {
		if got := stripInvalidFeishuCardImages(input); got != input {
			t.Fatalf("stripInvalidFeishuCardImages changed fenced source: %q -> %q", input, got)
		}
	}
	outside := stripInvalidFeishuCardImages("before ![x](https://example.com/a.png) after")
	if strings.Contains(outside, "![x]") {
		t.Fatalf("outside image was not stripped: %q", outside)
	}
}

func TestValidateMermaidPNGRejectsTruncatedImage(t *testing.T) {
	data := validTestPNG(t, 2, 2)
	if err := validateMermaidPNG(data[:33]); err == nil {
		t.Fatal("truncated PNG with valid IHDR was accepted")
	}
	corrupt := append([]byte(nil), data...)
	for i := 40; i < len(corrupt) && i < 80; i++ {
		corrupt[i] ^= 0xff
	}
	if err := validateMermaidPNG(corrupt); err == nil {
		t.Fatal("corrupt PNG payload was accepted")
	}
}

func TestRenderMermaidPNGReportsMissingAbsoluteRenderer(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist", "cc-connect-mermaid")
	p := &Platform{
		platformName:       "feishu",
		mermaidRenderer:    missing,
		mermaidTimeout:     2 * time.Second,
		mermaidTempDirBase: t.TempDir(),
	}
	_, err := p.renderMermaidPNG(context.Background(), "graph TD")
	if !errors.Is(err, errMermaidMissing) {
		t.Fatalf("error = %v, want missing renderer", err)
	}
}

func TestNewPlatformMermaidOptions(t *testing.T) {
	base := map[string]any{"app_id": "cli_test", "app_secret": "secret"}
	asPlatform := func(t *testing.T, opts map[string]any) *Platform {
		t.Helper()
		got, err := newPlatform("feishu", "https://open.feishu.cn", opts)
		if err != nil {
			t.Fatalf("newPlatform() error = %v", err)
		}
		switch p := got.(type) {
		case *Platform:
			return p
		case *interactivePlatform:
			return p.Platform
		default:
			t.Fatalf("unexpected platform type %T", got)
			return nil
		}
	}

	p := asPlatform(t, base)
	if p.mermaidResolver == nil {
		t.Fatal("mermaid resolver should be enabled by default")
	}
	if p.mermaidRenderer != mermaidDefaultRenderer {
		t.Fatalf("renderer = %q, want %q", p.mermaidRenderer, mermaidDefaultRenderer)
	}
	if p.mermaidTimeout != mermaidDefaultTimeout {
		t.Fatalf("timeout = %s, want %s", p.mermaidTimeout, mermaidDefaultTimeout)
	}

	disabled := asPlatform(t, map[string]any{
		"app_id": "cli_test", "app_secret": "secret", "render_mermaid": false,
	})
	if disabled.mermaidResolver != nil {
		t.Fatal("render_mermaid=false should not create a resolver")
	}

	custom := asPlatform(t, map[string]any{
		"app_id":               "cli_test",
		"app_secret":           "secret",
		"mermaid_renderer":     "/opt/bin/render-mermaid",
		"mermaid_timeout_secs": int64(7),
	})
	if custom.mermaidRenderer != "/opt/bin/render-mermaid" || custom.mermaidTimeout != 7*time.Second {
		t.Fatalf("custom options not applied: renderer=%q timeout=%s", custom.mermaidRenderer, custom.mermaidTimeout)
	}

	for _, bad := range []any{int64(0), int64(61), -1.0} {
		if _, err := newPlatform("feishu", "https://open.feishu.cn", map[string]any{
			"app_id": "cli_test", "app_secret": "secret", "mermaid_timeout_secs": bad,
		}); err == nil {
			t.Fatalf("mermaid_timeout_secs=%v should be rejected", bad)
		}
	}
}
