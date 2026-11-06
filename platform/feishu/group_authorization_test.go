package feishu

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func newGroupAuthorizationPlatform(t *testing.T, groups, allowChat string) (*interactivePlatform, <-chan *core.Message, <-chan string) {
	t.Helper()
	replies := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(t, w, map[string]any{"code": 0, "expire": 7200, "tenant_access_token": "test-token"})
		case strings.HasSuffix(r.URL.Path, "/reply"):
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read reply: %v", err)
			}
			replies <- string(body)
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"message_id": "om_reply"}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{}})
		}
	}))
	t.Cleanup(srv.Close)
	platform, err := New(map[string]any{
		"app_id": "cli_group_authorization", "app_secret": "test-secret", "domain": srv.URL,
		"allow_from": "ou_owner", "allow_group_members": groups, "allow_chat": allowChat,
		"group_reply_all": false, "render_mermaid": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := platform.(*interactivePlatform)
	p.botOpenID = "ou_bot"
	messages := make(chan *core.Message, 4)
	p.handler = func(_ core.Platform, msg *core.Message) { messages <- msg }
	for _, id := range []string{"ou_owner", "ou_member", "ou_new_member"} {
		p.userNameCache.Store(id, id)
	}
	for _, id := range []string{"oc_team", "oc_other", "oc_private"} {
		p.chatNameCache.Store(id, id)
	}
	return p, messages, replies
}

func groupAuthorizationMessage(user, chat, chatType string, mentioned bool) *larkim.P2MessageReceiveV1 {
	text := `{"text":"hello"}`
	msg := &larkim.EventMessage{
		MessageId: stringPtr("om_member_question"), ChatId: stringPtr(chat), ChatType: stringPtr(chatType),
		MessageType: stringPtr("text"), Content: &text,
		CreateTime: stringPtr(strconv.FormatInt(time.Now().UnixMilli(), 10)),
	}
	if mentioned {
		text = `{"text":"@_user_1 hello"}`
		msg.Mentions = []*larkim.MentionEvent{{Key: stringPtr("@_user_1"), Id: &larkim.UserId{OpenId: stringPtr("ou_bot")}}}
	}
	return &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: stringPtr(user)}, SenderType: stringPtr("user")},
		Message: msg,
	}}
}

func TestOnMessage_GroupMembersAllowedOnlyInConfiguredChat(t *testing.T) {
	cases := []struct {
		name, groups, allowChat, user, chat, chatType string
		mentioned, accepted, rejected                 bool
	}{
		{name: "member", groups: "oc_team", user: "ou_member", chat: "oc_team", chatType: "group", mentioned: true, accepted: true},
		{name: "new member needs no user entry", groups: "oc_team", user: "ou_new_member", chat: "oc_team", chatType: "group", mentioned: true, accepted: true},
		{name: "other group", groups: "oc_team", user: "ou_member", chat: "oc_other", chatType: "group", mentioned: true, rejected: true},
		{name: "private message", groups: "oc_team", user: "ou_member", chat: "oc_private", chatType: "p2p", rejected: true},
		{name: "p2p cannot use group grant", groups: "oc_team", user: "ou_member", chat: "oc_team", chatType: "p2p", rejected: true},
		{name: "mention still required", groups: "oc_team", user: "ou_member", chat: "oc_team", chatType: "group"},
		{name: "owner private access", groups: "oc_team", user: "ou_owner", chat: "oc_private", chatType: "p2p", accepted: true},
		{name: "owner other group access", groups: "oc_team", user: "ou_owner", chat: "oc_other", chatType: "group", mentioned: true, accepted: true},
		{name: "chat allowlist still applies", groups: "oc_team", allowChat: "oc_other", user: "ou_member", chat: "oc_team", chatType: "group", mentioned: true},
		{name: "default preserves user allowlist", user: "ou_member", chat: "oc_team", chatType: "group", mentioned: true, rejected: true},
		{name: "multiple group IDs", groups: " oc_other, oc_team ", user: "ou_member", chat: "oc_team", chatType: "group", mentioned: true, accepted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, messages, replies := newGroupAuthorizationPlatform(t, tc.groups, tc.allowChat)
			if err := p.onMessage(context.Background(), groupAuthorizationMessage(tc.user, tc.chat, tc.chatType, tc.mentioned)); err != nil {
				t.Fatal(err)
			}
			if tc.accepted {
				select {
				case msg := <-messages:
					if msg.UserID != tc.user || msg.Content != "hello" {
						t.Fatalf("unexpected message: %+v", msg)
					}
				case reply := <-replies:
					t.Fatalf("authorized member was rejected: %s", reply)
				case <-time.After(time.Second):
					t.Fatal("authorized message was not delivered")
				}
			} else {
				waitNoMessage(t, messages, 50*time.Millisecond, tc.name)
			}
			select {
			case reply := <-replies:
				if !tc.rejected || !strings.Contains(reply, core.UnauthorizedAccessMessage) {
					t.Fatalf("unexpected reply: %s", reply)
				}
			default:
				if tc.rejected {
					t.Fatal("missing authorization rejection")
				}
			}
		})
	}
}

func TestOnCardAction_GroupMembersStayScopedToConfiguredChat(t *testing.T) {
	for _, tc := range []struct {
		name, user, chat string
		accepted         bool
	}{
		{"member", "ou_member", "oc_team", true},
		{"member other group", "ou_member", "oc_other", false},
		{"member private chat", "ou_member", "oc_private", false},
		{"missing operator", "", "oc_team", false},
		{"missing chat", "ou_member", "", false},
		{"owner private chat", "ou_owner", "oc_private", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, messages, _ := newGroupAuthorizationPlatform(t, "oc_team", "")
			event := cardActionEvent(tc.user, "cmd:/help")
			event.Event.Context.OpenChatID = tc.chat
			if _, err := p.onCardAction(event); err != nil {
				t.Fatal(err)
			}
			if tc.accepted {
				msg := waitAnyMessage(t, messages, time.Second, tc.name)
				if msg.UserID != tc.user || msg.Content != "/help" {
					t.Fatalf("unexpected action: %+v", msg)
				}
			} else {
				waitNoMessage(t, messages, 50*time.Millisecond, tc.name)
			}
		})
	}
}

func TestNewPlatform_GroupMemberGrantsRequireExplicitChatIDs(t *testing.T) {
	for _, groups := range []string{"*", "oc_team, *"} {
		_, err := New(map[string]any{"app_id": "cli_group_grants", "app_secret": "test-secret", "allow_group_members": groups})
		if err == nil {
			t.Fatalf("wildcard group grant %q accepted", groups)
		}
	}
}
