package feishu

import (
	"context"
	"testing"
)

// Acceptance exercises the full outgoing sanitizer, because a safe renderer
// fallback is useful only if later Markdown transforms retain its source.
func TestMermaidAcceptance_FallbackPreservesFullFences(t *testing.T) {
	cases := map[string]string{
		"long backtick markers": "````mermaid\ninvalid diagram\n````\n",
		"tilde headings and blank lines": "~~~mermaid\n# source heading\n\n\n\ninvalid diagram\n~~~\n",
		"unfinished backtick source": "```mermaid\n# source heading\n\n\n\ninvalid diagram",
		"table-looking tilde source": "~~~mermaid\n| a | b |\n|---|---|\n| 1 | 2 |\n\n\n\n# source heading\n~~~\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			input := "before\n" + source
			if got := sanitizeCardMarkdownForCard(input); got != input {
				t.Fatalf("fallback code changed during card sanitation:\ngot  %q\nwant %q", got, input)
			}
		})
	}
}

func TestMermaidAcceptance_FailedSourceNeverDownloadsEmbeddedImages(t *testing.T) {
	p := newMermaidTestPlatform(func(context.Context, string) (string, error) {
		return "", errMermaidRenderFailed
	})
	t.Cleanup(p.mermaidResolver.Stop)
	p.richCardImageUploadFunc = func(context.Context, string) (string, error) {
		t.Error("image syntax inside failed Mermaid source triggered an upload")
		return "img_unexpected", nil
	}
	const source = "~~~mermaid\nA[\"![label](https://example.com/diagram.png)\"]\n# source heading\n\n\n\n~~~\n"
	resolved := p.ResolveRichCardMarkdown(context.Background(), source, true)
	if got := sanitizeCardMarkdownForCard(resolved); got != source {
		t.Fatalf("failed Mermaid source changed:\ngot  %q\nwant %q", got, source)
	}
}
