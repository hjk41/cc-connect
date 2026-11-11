package core

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

type replyAllTestPlatform struct {
	stubPlatformEngine
	replyMu sync.Mutex
	chats   string
	global  bool
}

func (p *replyAllTestPlatform) ReplyAllChats() (string, bool) {
	p.replyMu.Lock()
	defer p.replyMu.Unlock()
	return p.chats, p.global
}

func (p *replyAllTestPlatform) SetReplyAllChats(chats string) error {
	if strings.Contains(chats, "*") {
		return errors.New("wildcard not supported")
	}
	p.replyMu.Lock()
	p.chats = chats
	p.replyMu.Unlock()
	return nil
}

func TestConfigReplyAllChatsOperationsAndPersistence(t *testing.T) {
	p := &replyAllTestPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	var saved []string
	e.SetPlatformReplyAllChatsSaveFunc(func(platformName, chats string) error {
		if platformName != "test" {
			t.Fatalf("platform = %q", platformName)
		}
		saved = append(saved, chats)
		return nil
	})
	msg := &Message{ChatType: "group", ChannelID: "oc_team", SessionKey: "test:thread:user", ReplyCtx: "ctx"}
	for _, tc := range []struct{ raw, want string }{
		{"+$this", "oc_team"},
		{"+oc_other,oc_team", "oc_team,oc_other"},
		{"-$this", "oc_other"},
		{"oc_replacement", "oc_replacement"},
		{"none", ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			e.cmdConfig(p, msg, []string{"set", "reply_all_chats", tc.raw})
			got, _ := p.ReplyAllChats()
			if got != tc.want || saved[len(saved)-1] != tc.want {
				t.Fatalf("runtime=%q saved=%v want=%q", got, saved, tc.want)
			}
		})
	}
	e.cmdConfig(p, msg, []string{"get", "reply_all_chats"})
	if sent := p.getSent(); !strings.Contains(sent[len(sent)-1], "none") {
		t.Fatalf("empty list reply: %v", sent)
	}
}

func TestConfigReplyAllChatsRejectsInvalidAndRestoresAfterSaveFailure(t *testing.T) {
	p := &replyAllTestPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}, chats: "oc_original"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{ChatType: "p2p", ReplyCtx: "ctx"}
	for _, raw := range []string{"+$this", "*", "@member"} {
		e.cmdConfig(p, msg, []string{"set", "reply_all_chats", raw})
		if got, _ := p.ReplyAllChats(); got != "oc_original" {
			t.Fatalf("%q changed list to %q", raw, got)
		}
	}
	e.SetPlatformReplyAllChatsSaveFunc(func(string, string) error { return errors.New("disk full") })
	e.cmdConfig(p, msg, []string{"set", "reply_all_chats", "+oc_other"})
	if got, _ := p.ReplyAllChats(); got != "oc_original" {
		t.Fatalf("failed save left %q active", got)
	}
	if sent := p.getSent(); sent[len(sent)-1] != e.i18n.T(MsgReplyAllChatsSaveFailed) {
		t.Fatalf("save failure reply: %v", sent)
	}
}

func TestConfigReplyAllChatsCapabilityAndGlobalWarning(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.cmdConfig(p, &Message{ReplyCtx: "ctx"}, []string{"get", "reply_all_chats"})
	if sent := p.getSent(); sent[len(sent)-1] != e.i18n.T(MsgReplyAllChatsUnsupported) {
		t.Fatalf("unsupported reply: %v", sent)
	}
	p2 := &replyAllTestPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}, global: true}
	e.cmdConfig(p2, &Message{ReplyCtx: "ctx"}, []string{"get", "reply_all_chats"})
	if sent := p2.getSent(); !strings.Contains(sent[len(sent)-1], "group_reply_all") {
		t.Fatalf("global warning missing: %v", sent)
	}
}
