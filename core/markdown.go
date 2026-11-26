package core

import (
	"regexp"
	"strings"
)

var (
	reCodeBlock  = regexp.MustCompile("(?s)```[a-zA-Z]*\n?(.*?)```")
	reInlineCode = regexp.MustCompile("`([^`]+)`")
	reBoldAst    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalicAst  = regexp.MustCompile(`\*(.+?)\*`)
	reStrike     = regexp.MustCompile(`~~(.+?)~~`)
	reLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reHeading    = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	reHorizontal = regexp.MustCompile(`(?m)^---+\s*$`)
	reBlockquote = regexp.MustCompile(`(?m)^>\s?`)
)

// NeutralizeMarkdownFenceRuns rewrites every run of three or more backticks
// so it cannot be interpreted as a Markdown code-fence marker. A zero-width
// space is inserted between backticks; the visual text remains essentially
// unchanged while nested tool input/output cannot terminate the outer fence.
func NeutralizeMarkdownFenceRuns(s string) string {
	if !strings.Contains(s, "```") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		if s[i] != '`' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		n := j - i
		if n < 3 {
			b.WriteString(s[i:j])
			i = j
			continue
		}
		for k := 0; k < n; k++ {
			if k > 0 {
				b.WriteString("\u200b")
			}
			b.WriteByte('`')
		}
		i = j
	}
	return b.String()
}

// NeutralizeHTMLTagOpeners inserts a zero-width space after '<' when it
// starts something that looks like an HTML tag/declaration. Feishu card
// Markdown can reject unrecognized tags even when they appear in otherwise
// untrusted tool text; the invisible separator preserves the visual text.
func NeutralizeHTMLTagOpeners(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		b.WriteByte(s[i])
		if s[i] != '<' || i+1 >= len(s) {
			continue
		}
		c := s[i+1]
		if c == '/' || c == '!' || c == '?' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			b.WriteString("\u200b")
		}
	}
	return b.String()
}

// SafeMarkdownCodeFence wraps arbitrary text in a Markdown code fence while
// neutralizing fence markers and HTML-like tag openers inside the text. Use
// this for untrusted tool input/output; embedding it raw can corrupt or
// invalidate the surrounding Markdown document.
func SafeMarkdownCodeFence(text, lang string) string {
	fence := "```"
	if strings.TrimSpace(lang) != "" {
		fence += strings.TrimSpace(lang)
	}
	return fence + "\n" + NeutralizeMarkdownFenceRuns(NeutralizeHTMLTagOpeners(text)) + "\n```"
}

// SafeMarkdownInlineCode renders arbitrary short text as inline code without
// allowing backticks in the text to terminate the inline-code span.
func SafeMarkdownInlineCode(text string) string {
	return "`" + strings.ReplaceAll(strings.TrimSpace(text), "`", "'") + "`"
}

// StripMarkdown converts Markdown-formatted text to clean plain text.
// Useful for platforms that don't support Markdown rendering (WeChat, LINE, etc.).
//
// Note: the underscore forms of bold (`__bold__`) and italic (`_italic_`) are
// intentionally not stripped. The patterns `_..._` and `__...__` are
// indistinguishable from snake_case and dunder identifiers (`my_func_name`,
// Python's `__init__`), and stripping them silently corrupted code-related
// text into things like `myfuncname` or `init`. Agents reliably emit the
// asterisk forms (`**bold**`, `*italic*`), so dropping the underscore forms
// is the safe trade-off here. Inputs containing literal `_italic_` will keep
// their underscores; this is a small cosmetic loss vs. losing identifier
// integrity in TTS / LINE / WeChat output.
func StripMarkdown(s string) string {
	// Preserve code block content but remove fences
	s = reCodeBlock.ReplaceAllString(s, "$1")

	// Inline code — remove backticks
	s = reInlineCode.ReplaceAllString(s, "$1")

	// Bold / italic / strikethrough — keep text
	s = reBoldAst.ReplaceAllString(s, "$1")
	s = reItalicAst.ReplaceAllString(s, "$1")
	s = reStrike.ReplaceAllString(s, "$1")

	// Links [text](url) → text (url)
	s = reLink.ReplaceAllString(s, "$1 ($2)")

	// Headings — remove # prefix
	s = reHeading.ReplaceAllString(s, "")

	// Horizontal rules
	s = reHorizontal.ReplaceAllString(s, "")

	// Blockquotes
	s = reBlockquote.ReplaceAllString(s, "")

	// Collapse 3+ consecutive blank lines into 2
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}
