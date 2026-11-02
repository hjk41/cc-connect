package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	lark "github.com/larksuite/oapi-sdk-go/v3"
)

type capturedMessage struct {
	msgType string
	content string
}

func newMermaidIntegrationPlatform(t *testing.T, render func(context.Context, string) (string, error)) (*Platform, *[]capturedMessage, *sync.Mutex) {
	t.Helper()
	var (
		mu       sync.Mutex
		captured []capturedMessage
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(t, w, map[string]any{"code": 0, "expire": 7200, "tenant_access_token": "t"})
		case strings.HasSuffix(r.URL.Path, "/reply") && r.Method == http.MethodPost,
			r.URL.Path == "/open-apis/im/v1/messages" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var req struct {
				MsgType string `json:"msg_type"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("unmarshal request: %v", err)
			}
			mu.Lock()
			captured = append(captured, capturedMessage{msgType: req.MsgType, content: req.Content})
			mu.Unlock()
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"message_id": "om_ok"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{}})
		}
	}))
	t.Cleanup(srv.Close)

	p := &Platform{
		platformName: "feishu",
		domain:       srv.URL,
		appID:        "cli_mermaid",
		appSecret:    "secret",
		client: lark.NewClient("cli_mermaid", "secret",
			lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client())),
		replayClient: lark.NewClient("cli_mermaid", "secret",
			lark.WithEnableTokenCache(false), lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client())),
	}
	p.mermaidResolver = newMermaidResolver("test", time.Second, render)
	return p, &captured, &mu
}

func TestMermaidReplyPathEmbedsImage(t *testing.T) {
	p, captured, mu := newMermaidIntegrationPlatform(t, func(context.Context, string) (string, error) {
		return "img_v3_reply", nil
	})
	input := "before\n```mermaid\ngraph TD\nA-->B\n```\nafter"
	err := p.Reply(context.Background(), replyContext{messageID: "om_root", chatID: "oc_chat"}, input)
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("captured %d messages, want 1", len(*captured))
	}
	msg := (*captured)[0]
	if msg.msgType != "interactive" {
		t.Fatalf("msg type = %q, want interactive", msg.msgType)
	}
	if !strings.Contains(msg.content, "img_v3_reply") || strings.Contains(msg.content, "graph TD") {
		t.Fatalf("reply card should embed rendered image, got %s", msg.content)
	}
	if !strings.Contains(msg.content, "before") || !strings.Contains(msg.content, "after") {
		t.Fatalf("reply card should preserve surrounding text, got %s", msg.content)
	}
}

func TestMermaidSendAndFooterPathsEmbedImage(t *testing.T) {
	p, captured, mu := newMermaidIntegrationPlatform(t, func(context.Context, string) (string, error) {
		return "img_v3_send", nil
	})
	input := "```mermaid\nsequenceDiagram\nA->>B: hi\n```"
	if err := p.Send(context.Background(), replyContext{chatID: "oc_chat"}, input); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if err := p.SendWithStatusFooter(context.Background(), replyContext{messageID: "om_root", chatID: "oc_chat"}, input, "done"); err != nil {
		t.Fatalf("SendWithStatusFooter() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 2 {
		t.Fatalf("captured %d messages, want 2", len(*captured))
	}
	for i, msg := range *captured {
		if !strings.Contains(msg.content, "img_v3_send") || strings.Contains(msg.content, "sequenceDiagram") {
			t.Fatalf("message %d should embed rendered image, got %s", i, msg.content)
		}
	}
	if !strings.Contains((*captured)[1].content, "done") {
		t.Fatalf("footer message lost footer text: %s", (*captured)[1].content)
	}
}

func TestMermaidMentionMessagesStayPlainText(t *testing.T) {
	p, captured, mu := newMermaidIntegrationPlatform(t, func(context.Context, string) (string, error) {
		t.Fatal("mermaid renderer must not run for @mention text messages")
		return "", nil
	})
	input := `<at user_id="ou_bot">Bot</at> please review\n\n` + "```mermaid\ngraph TD\n```"
	if err := p.Reply(context.Background(), replyContext{messageID: "om_root", chatID: "oc_chat"}, input); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("captured %d messages, want 1", len(*captured))
	}
	msg := (*captured)[0]
	if msg.msgType != "text" {
		t.Fatalf("msg type = %q, want text", msg.msgType)
	}
	if !strings.Contains(msg.content, "```mermaid") {
		t.Fatalf("@mention message should keep original source, got %s", msg.content)
	}
}

func TestMermaidRichCardAndStreamingTextPaths(t *testing.T) {
	p := newMermaidTestPlatform(func(context.Context, string) (string, error) {
		return "img_v3_rich", nil
	})
	input := "**中文句子。**NLB\n\n```mermaid\ngraph TD\nA-->B\n```"
	resolved := p.ResolveRichCardMarkdown(context.Background(), input, true)
	cardJSON := p.BuildRichCard(core.CardStatusDone, "", nil, resolved, false, "")
	if !strings.Contains(cardJSON, "img_v3_rich") {
		t.Fatalf("rich card missing image key: %s", cardJSON)
	}
	if !strings.Contains(cardJSON, "**中文句子。** NLB") {
		t.Fatalf("rich card did not normalize emphasis boundary: %s", cardJSON)
	}

	var gotContent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			writeJSON(t, w, map[string]any{"code": 0, "expire": 7200, "tenant_access_token": "t"})
			return
		}
		if strings.Contains(r.URL.Path, "/elements/") && r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotContent)
			writeJSON(t, w, map[string]any{"code": 0})
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
		writeJSON(t, w, map[string]any{"code": 0})
	}))
	defer srv.Close()
	p.domain = srv.URL
	p.client = lark.NewClient("cli_stream", "secret",
		lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client()))

	handle := &feishuPreviewHandle{messageID: "om_card", chatID: "oc_chat", cardID: "card_x"}
	if err := p.StreamRichCardText(context.Background(), handle, "**中文句子。**NLB"); err != nil {
		t.Fatalf("StreamRichCardText() error = %v", err)
	}
	content, _ := gotContent["content"].(string)
	if content != "**中文句子。** NLB" {
		t.Fatalf("streaming element content = %q, want normalized text", content)
	}
}
