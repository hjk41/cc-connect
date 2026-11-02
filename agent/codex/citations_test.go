package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripCodexFileCitations_ReplacesMarkersWithBasenames(t *testing.T) {
	text := "已生成 V12。\n\n" +
		`:codex-file-citation{path="/Users/hct/finsearch/deliverables/厚笃科技 V12.pptx" purpose="output"}` + "\n" +
		`参考 :codex-file-citation{path="/Users/hct/finsearch/in/old.pptx" purpose="source"} 的版式。`

	clean, citations := stripCodexFileCitations(text)
	if strings.Contains(clean, ":codex-file-citation{") {
		t.Fatalf("citation marker leaked into clean text: %q", clean)
	}
	if !strings.Contains(clean, "`厚笃科技 V12.pptx`") {
		t.Fatalf("output basename missing from clean text: %q", clean)
	}
	if !strings.Contains(clean, "`old.pptx`") {
		t.Fatalf("source basename missing from clean text: %q", clean)
	}
	if len(citations) != 2 {
		t.Fatalf("citations = %#v, want 2", citations)
	}
	if citations[0].Purpose != "output" || citations[1].Purpose != "source" {
		t.Fatalf("purposes = %#v", citations)
	}
}

func TestStripCodexFileCitations_NoMarkerIsNoop(t *testing.T) {
	text := "plain reply"
	clean, citations := stripCodexFileCitations(text)
	if clean != text || len(citations) != 0 {
		t.Fatalf("clean=%q citations=%#v", clean, citations)
	}
}

func TestCodexOutputAttachments_OnlyFilesInsideWorkspace(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()

	inside := filepath.Join(workDir, "deck.pptx")
	outside := filepath.Join(outsideDir, "secret.pptx")
	if err := os.WriteFile(inside, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	text := "done\n" +
		`:codex-file-citation{path="` + inside + `" purpose="output"}` + "\n" +
		`:codex-file-citation{path="` + outside + `" purpose="output"}` + "\n" +
		`:codex-file-citation{path="` + inside + `" purpose="source"}`

	clean, paths := codexOutputAttachments(text, workDir)
	wantInside, _ := filepath.EvalSymlinks(inside)
	if len(paths) != 1 || paths[0] != wantInside {
		t.Fatalf("paths = %#v, want only %q", paths, wantInside)
	}
	if strings.Contains(clean, ":codex-file-citation{") {
		t.Fatalf("citation marker leaked: %q", clean)
	}
}

func TestCodexOutputAttachments_RelativePathAndDedupe(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workDir, "out", "report.pdf")
	if err := os.WriteFile(path, []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}

	text := `:codex-file-citation{path="out/report.pdf" purpose="output"}` + "\n" +
		`:codex-file-citation{path="out/report.pdf" purpose="output"}`

	_, paths := codexOutputAttachments(text, workDir)
	wantPath, _ := filepath.EvalSymlinks(path)
	if len(paths) != 1 || paths[0] != wantPath {
		t.Fatalf("paths = %#v, want [%q]", paths, wantPath)
	}
}

func TestCodexOutputAttachments_RejectsMissingAndDirectory(t *testing.T) {
	workDir := t.TempDir()
	dir := filepath.Join(workDir, "sub")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	text := `:codex-file-citation{path="missing.pptx" purpose="output"}` + "\n" +
		`:codex-file-citation{path="sub" purpose="output"}`

	_, paths := codexOutputAttachments(text, workDir)
	if len(paths) != 0 {
		t.Fatalf("paths = %#v, want none", paths)
	}
}

func TestCodexOutputAttachments_SymlinkEscapeRejected(t *testing.T) {
	workDir := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.pptx")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workDir, "link.pptx")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	text := `:codex-file-citation{path="link.pptx" purpose="output"}`
	_, paths := codexOutputAttachments(text, workDir)
	if len(paths) != 0 {
		t.Fatalf("symlink escape was attached: %#v", paths)
	}
}

func TestDedupeAttachmentPaths_PreservesOrder(t *testing.T) {
	got := dedupeAttachmentPaths([]string{"/a", "/b", "/a", "", "/b", "/c"})
	want := []string{"/a", "/b", "/c"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}
