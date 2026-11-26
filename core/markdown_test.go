package core

import (
	"strings"
	"testing"
)

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// Asterisk forms keep working — these are what agents typically emit.
		{"bold asterisk", "this is **bold** text", "this is bold text"},
		{"italic asterisk", "this is *italic* text", "this is italic text"},
		{"bold italic combined", "**bold** and *italic*", "bold and italic"},

		// Strikethrough, links, headings, code — unchanged behavior.
		{"strikethrough", "~~struck~~", "struck"},
		{"link", "[GitHub](https://github.com)", "GitHub (https://github.com)"},
		{"heading", "# Title\nbody", "Title\nbody"},
		{"inline code", "use `os.path.join`", "use os.path.join"},
		{"fenced code block", "```go\nfmt.Println(\"hi\")\n```", "fmt.Println(\"hi\")"},

		// Regression: underscore forms must NOT be stripped, since the same
		// patterns appear in legitimate identifiers. Stripping them used to
		// produce e.g. "mysnakecase_var" and "init", corrupting code
		// references in TTS / LINE / WeChat output.
		{"snake_case identifier", "call my_func_name() to start", "call my_func_name() to start"},
		{"two-underscore snake_case", "use module_name_var here", "use module_name_var here"},
		{"python dunder", "Python's __init__ method", "Python's __init__ method"},
		{"version dunder", "the __version__ string", "the __version__ string"},
		{"single underscore identifier", "function_name", "function_name"},

		// Underscore-form bold/italic is now kept literal (rare in practice).
		{"underscore italic kept literal", "_italic_ text", "_italic_ text"},
		{"underscore bold kept literal", "__bold__ text", "__bold__ text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMarkdown(tt.in)
			if got != tt.want {
				t.Errorf("StripMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSafeMarkdownCodeFence_NeutralizesNestedFenceRuns(t *testing.T) {
	input := "before\n```python\nprint('inner')\n```\nafter"
	got := SafeMarkdownCodeFence(input, "text")
	if !strings.HasPrefix(got, "```text\n") || !strings.HasSuffix(got, "\n```") {
		t.Fatalf("safe fence wrapper missing: %q", got)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(got, "```text\n"), "\n```")
	if strings.Contains(body, "```") {
		t.Fatalf("nested fence run was not neutralized: %q", body)
	}
	if !strings.Contains(body, "`\u200b`\u200b`") {
		t.Fatalf("neutralized fence markers should remain visually recognizable: %q", body)
	}

	four := SafeMarkdownCodeFence("x ```` y", "")
	body = strings.TrimSuffix(strings.TrimPrefix(four, "```\n"), "\n```")
	if strings.Contains(body, "```") {
		t.Fatalf("four-backtick run left a closing fence behind: %q", body)
	}
}

func TestSafeMarkdownInlineCode_EscapesBackticks(t *testing.T) {
	if got := SafeMarkdownInlineCode("echo `x`"); got != "`echo 'x'`" {
		t.Fatalf("SafeMarkdownInlineCode() = %q", got)
	}
}

func TestNeutralizeHTMLTagOpeners(t *testing.T) {
	in := "if a < b { print(<a href=\"x\">text</a> <![CDATA[x]]> <?xml>) }"
	got := NeutralizeHTMLTagOpeners(in)
	if !strings.Contains(got, "<\u200ba href") || !strings.Contains(got, "<\u200b/a>") {
		t.Fatalf("tag openers not neutralized: %q", got)
	}
	if !strings.Contains(got, "a < b") {
		t.Fatalf("comparison operator should remain untouched: %q", got)
	}
	fenced := SafeMarkdownCodeFence("<script>alert(1)</script>", "text")
	if !strings.Contains(fenced, "<\u200bscript>") {
		t.Fatalf("safe fence should neutralize HTML-like tags: %q", fenced)
	}
}
