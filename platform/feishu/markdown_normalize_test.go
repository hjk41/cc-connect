package feishu

import (
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func countStrongNodes(md string) int {
	doc := goldmark.DefaultParser().Parse(text.NewReader([]byte(md)))
	count := 0
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if emphasis, ok := node.(*ast.Emphasis); ok && emphasis.Level == 2 {
			count++
		}
		return ast.WalkContinue, nil
	})
	return count
}

func TestNormalizeFeishuMarkdown_ReportedChinesePunctuationBoundary(t *testing.T) {
	const raw = "**中文句子。**NLB"
	if got := countStrongNodes(raw); got != 0 {
		t.Fatalf("pre-fix goldmark strong nodes = %d, want 0 (reproduces the reported bug)", got)
	}

	const want = "**中文句子。** NLB"
	if got := normalizeFeishuMarkdown(raw); got != want {
		t.Fatalf("normalizeFeishuMarkdown() = %q, want %q", got, want)
	}
	if got := countStrongNodes(want); got != 1 {
		t.Fatalf("post-repair goldmark strong nodes = %d, want 1", got)
	}
}

func TestNormalizeFeishuMarkdown_PreservesValidAndEscapedMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "already valid", in: "**bold** text"},
		{name: "valid intraword", in: "中文**加粗**后文"},
		{name: "punctuation followed by space", in: "**中文句子。** NLB"},
		{name: "punctuation followed by punctuation", in: "**中文句子。**，下句"},
		{name: "escaped opener", in: `\**中文句子。**NLB`},
		{name: "triple emphasis", in: "***中文句子。***NLB"},
		{name: "unpaired", in: "**中文句子。NLB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeFeishuMarkdown(tt.in); got != tt.in {
				t.Fatalf("normalizeFeishuMarkdown(%q) = %q, want unchanged", tt.in, got)
			}
		})
	}
}

func TestNormalizeFeishuMarkdown_ChineseQuotesAndMultipleSpans(t *testing.T) {
	in := "**“中文句子。”**下一步和**第二句！**继续"
	want := "**“中文句子。”** 下一步和**第二句！** 继续"
	got := normalizeFeishuMarkdown(in)
	if got != want {
		t.Fatalf("normalizeFeishuMarkdown() = %q, want %q", got, want)
	}
	if n := countStrongNodes(got); n != 2 {
		t.Fatalf("goldmark strong nodes = %d, want 2", n)
	}
}

func TestNormalizeFeishuMarkdown_OpenerBoundary(t *testing.T) {
	in := "NLB**。中文。**"
	want := "NLB **。中文。**"
	got := normalizeFeishuMarkdown(in)
	if got != want {
		t.Fatalf("normalizeFeishuMarkdown() = %q, want %q", got, want)
	}
	if n := countStrongNodes(got); n != 1 {
		t.Fatalf("goldmark strong nodes = %d, want 1", n)
	}
}

func TestNormalizeFeishuMarkdown_IgnoresCodeAndDestinations(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "inline code", in: "`**中文句子。**NLB`"},
		{name: "fenced backticks", in: "```text\n**中文句子。**NLB\n```"},
		{name: "fenced tildes", in: "~~~\n**中文句子。**NLB\n~~~"},
		{name: "unclosed fence", in: "```text\n**中文句子。**NLB"},
		{name: "link destination", in: "[**中文句子。**NLB](https://example.com/**a。**B)"},
		{name: "image destination", in: "![alt](https://example.com/**a。**B)"},
		{name: "reference definition", in: "[x]: https://example.com/**a。**B"},
		{name: "angle autolink", in: "<https://example.com/**a。**B>"},
		{name: "mailto autolink", in: "<mailto:user@example.com>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeFeishuMarkdown(tt.in)
			if tt.name == "link destination" || tt.name == "image destination" {
				if !strings.Contains(got, "https://example.com/**a。**B") {
					t.Fatalf("link destination was changed: %q", got)
				}
				return
			}
			if tt.name == "reference definition" {
				if got != tt.in {
					t.Fatalf("reference definition changed: %q -> %q", tt.in, got)
				}
				return
			}
			if tt.name == "angle autolink" || tt.name == "mailto autolink" {
				if got != tt.in {
					t.Fatalf("autolink changed: %q -> %q", tt.in, got)
				}
				return
			}
			if got != tt.in {
				t.Fatalf("normalizeFeishuMarkdown() = %q, want unchanged %q", got, tt.in)
			}
		})
	}
}

func TestNormalizeFeishuMarkdown_Idempotent(t *testing.T) {
	inputs := []string{
		"**中文句子。**NLB",
		"**“中文句子。”**下一步和**第二句！**继续",
		"NLB**。中文。**",
		"plain **bold** text",
		"```\n**中文句子。**NLB\n```",
	}
	for _, in := range inputs {
		once := normalizeFeishuMarkdown(in)
		twice := normalizeFeishuMarkdown(once)
		if twice != once {
			t.Fatalf("normalize twice changed %q -> %q -> %q", in, once, twice)
		}
	}
}

func TestNormalizeFeishuMarkdown_PreservesSurroundingText(t *testing.T) {
	in := "前文 **中文句子。**NLB 后文"
	want := "前文 **中文句子。** NLB 后文"
	if got := normalizeFeishuMarkdown(in); got != want {
		t.Fatalf("normalizeFeishuMarkdown() = %q, want %q", got, want)
	}
}
