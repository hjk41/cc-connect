package feishu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMermaidRealRendererIntegration exercises the checked-in Node renderer and
// the Go process/PNG/upload pipeline. It is opt-in because it requires npm
// dependencies and a locally installed Chrome/Chromium.
func TestMermaidRealRendererIntegration(t *testing.T) {
	if os.Getenv("CC_MERMAID_REAL_TEST") == "" {
		t.Skip("set CC_MERMAID_REAL_TEST=1 to run the local renderer integration test")
	}
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is unix-only")
	}
	renderer, err := filepath.Abs(filepath.Join("..", "..", "tools", "mermaid-renderer", "cc-connect-mermaid"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renderer); err != nil {
		t.Skipf("renderer not installed: %v", err)
	}

	p := &Platform{
		platformName:    "feishu",
		mermaidRenderer: renderer,
		mermaidTimeout:  30 * time.Second,
		mermaidUploadFunc: func(_ context.Context, data []byte) (string, error) {
			if err := validateMermaidPNG(data); err != nil {
				return "", err
			}
			return "img_v3_real", nil
		},
	}

	for _, name := range []string{"chinese-flowchart", "sequence"} {
		source, err := os.ReadFile(filepath.Join("..", "..", "tools", "mermaid-renderer", "fixtures", name+".mmd"))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		key, err := p.renderMermaidPNG(context.Background(), string(source))
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		if key != "img_v3_real" {
			t.Fatalf("render %s key = %q", name, key)
		}
	}

	for _, name := range []string{"invalid", "directive"} {
		source, err := os.ReadFile(filepath.Join("..", "..", "tools", "mermaid-renderer", "fixtures", name+".mmd"))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if _, err := p.renderMermaidPNG(context.Background(), string(source)); err == nil {
			t.Fatalf("fixture %s should fail rendering", name)
		}
	}
}

// TestMermaidRealRendererTimeoutCleanup proves a real Chrome render cancelled
// by the Go timeout leaves no owned Node/Chrome process or profile directory.
func TestMermaidRealRendererTimeoutCleanup(t *testing.T) {
	if os.Getenv("CC_MERMAID_REAL_TEST") == "" {
		t.Skip("set CC_MERMAID_REAL_TEST=1 to run the local renderer integration test")
	}
	if runtime.GOOS == "windows" {
		t.Skip("process inspection is unix-only")
	}
	renderer, err := filepath.Abs(filepath.Join("..", "..", "tools", "mermaid-renderer", "cc-connect-mermaid"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renderer); err != nil {
		t.Skipf("renderer not installed: %v", err)
	}

	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&b, "N%d-->N%d\n", i, i+1)
	}

	tempBase := t.TempDir()
	p := &Platform{
		platformName:       "feishu",
		mermaidRenderer:    renderer,
		mermaidTimeout:     1500 * time.Millisecond,
		mermaidTempDirBase: tempBase,
		mermaidUploadFunc: func(context.Context, []byte) (string, error) {
			t.Fatal("upload must not run after timeout")
			return "", nil
		},
	}
	start := time.Now()
	_, err = p.renderMermaidPNG(context.Background(), b.String())
	if !errors.Is(err, errMermaidTimeout) {
		t.Skipf("render finished before timeout (%v after %s); cannot prove cancellation cleanup", err, time.Since(start))
	}

	// Give Puppeteer/Chrome a moment to reap after SIGTERM/SIGKILL.
	deadline := time.Now().Add(12 * time.Second)
	for {
		out, psErr := exec.Command("ps", "-axo", "command=").Output()
		if psErr != nil {
			t.Skipf("ps unavailable: %v", psErr)
		}
		var owned []string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "--user-data-dir=") && strings.Contains(line, "cc-connect-mermaid-profile-") {
				owned = append(owned, line)
			}
			if strings.Contains(line, renderer+" --input") {
				owned = append(owned, line)
			}
		}
		if len(owned) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("owned renderer/Chrome process survived cancellation for %s:\n%s",
				time.Since(start), strings.Join(owned, "\n"))
		}
		time.Sleep(100 * time.Millisecond)
	}

	entries, err := os.ReadDir(tempBase)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "cc-connect-mermaid-") {
			t.Fatalf("temporary render/profile directory leaked: %s", entry.Name())
		}
	}
}
