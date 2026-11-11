package feishu

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestReplyAllChatsConstructorAndValidation(t *testing.T) {
	for _, raw := range []any{"*", "oc_team,*", "oc_team:topic:om_root", "ou_owner", true, []string{"oc_team"}} {
		if _, err := New(map[string]any{"app_id": "test", "app_secret": "secret", "reply_all_chats": raw}); err == nil {
			t.Fatalf("accepted invalid reply_all_chats=%v", raw)
		}
	}
	platform, err := New(map[string]any{"app_id": "test", "app_secret": "secret", "reply_all_chats": " oc_team,oc_other,oc_team "})
	if err != nil {
		t.Fatal(err)
	}
	p := platform.(*interactivePlatform)
	if chats, global := p.ReplyAllChats(); chats != "oc_team,oc_other" || global {
		t.Fatalf("initial config = %q/%v", chats, global)
	}
	if err := p.SetReplyAllChats("*"); err == nil {
		t.Fatal("wildcard accepted")
	}
	if chats, _ := p.ReplyAllChats(); chats != "oc_team,oc_other" {
		t.Fatal("invalid update mutated list")
	}
}

func TestReplyAllChatsCurrentGroupUsesChatIDInTopics(t *testing.T) {
	p := &Platform{platformName: "feishu", threadIsolation: true}
	msg := &core.Message{ReplyCtx: replyContext{chatID: "oc_team", sessionKey: "feishu:oc_team:thread:om_root"}}
	p.populateWorkspaceChannelKeys(msg)
	if msg.ChannelID != "oc_team" {
		t.Fatalf("group command target = %q, want parent chat", msg.ChannelID)
	}
	if msg.ChannelKey == msg.ChannelID {
		t.Fatal("test did not exercise topic workspace isolation")
	}
}

func TestOnMessage_ReplyAllChatsRuntimeScopeAndAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, chat, user, allowChat  string
		mentioned, history, accepted bool
	}{
		{name: "listed group", chat: "oc_team", user: "ou_owner", accepted: true},
		{name: "listed history-sharing group", chat: "oc_team", user: "ou_owner", history: true, accepted: true},
		{name: "unlisted group", chat: "oc_other", user: "ou_owner"},
		{name: "unlisted mentioned group", chat: "oc_other", user: "ou_owner", mentioned: true, accepted: true},
		{name: "user authorization retained", chat: "oc_team", user: "ou_member"},
		{name: "chat authorization retained", chat: "oc_team", user: "ou_owner", allowChat: "oc_other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, messages, _ := newGroupAuthorizationPlatform(t, "", tc.allowChat)
			p.groupChatHistoryShare = tc.history
			if err := p.SetReplyAllChats("oc_team"); err != nil {
				t.Fatal(err)
			}
			if err := p.onMessage(context.Background(), groupAuthorizationMessage(tc.user, tc.chat, "group", tc.mentioned)); err != nil {
				t.Fatal(err)
			}
			if tc.accepted {
				select {
				case msg := <-messages:
					if msg.Content != "hello" {
						t.Fatalf("unexpected content %q", msg.Content)
					}
				case <-time.After(time.Second):
					t.Fatal("allowed message not delivered")
				}
			} else {
				waitNoMessage(t, messages, 30*time.Millisecond, tc.name)
			}
		})
	}
	// Removing the current group takes effect on the next inbound event.
	p, messages, _ := newGroupAuthorizationPlatform(t, "", "")
	_ = p.SetReplyAllChats("oc_team")
	_ = p.SetReplyAllChats("")
	if err := p.onMessage(context.Background(), groupAuthorizationMessage("ou_owner", "oc_team", "group", false)); err != nil {
		t.Fatal(err)
	}
	waitNoMessage(t, messages, 30*time.Millisecond, "removed group")
	// Legacy global behavior still overrides an empty list.
	p.groupReplyAll = true
	if !p.repliesToAllInChat("oc_other") {
		t.Fatal("global reply-all no longer applies")
	}
}

func TestReplyAllChatsConcurrentReadWrite(t *testing.T) {
	p := &Platform{allowFrom: "ou_owner"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := p.SetReplyAllChats(fmt.Sprintf("oc_%d", i)); err != nil {
					t.Error(err)
				}
				p.ReplyAllChats()
				p.repliesToAllInChat("oc_0")
				p.isSenderAllowed("ou_owner", "oc_0")
			}
		}(i)
	}
	wg.Wait()
}

func TestReplyAllChatsHistoryDoesNotCaptureTriggeredMessages(t *testing.T) {
	messages := make(chan *core.Message, 2)
	p := newGroupHistoryTestPlatform(func(_ core.Platform, msg *core.Message) { messages <- msg })
	if err := p.SetReplyAllChats("oc_main"); err != nil {
		t.Fatal(err)
	}
	sendGroupHistoryText(t, p, "om_reply_all", "ou_alice", "direct question", "oc_main", "", false)
	select {
	case msg := <-messages:
		if msg.ExtraContent != "" || msg.Content != "direct question" {
			t.Fatalf("triggered message became overheard history: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("history sharing swallowed mention-free message")
	}
	if snapshot := p.snapshotGroupHistory("chat:oc_main"); len(snapshot.entries) != 0 {
		t.Fatal("triggered message stored as group history")
	}
}
