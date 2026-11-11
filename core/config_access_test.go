package core

import (
	"strings"
	"sync"
	"testing"
)

type accessTestPlatform struct {
	stubPlatformEngine
	mu                sync.Mutex
	allowFrom         string
	allowGroupMembers string
}

func (p *accessTestPlatform) AccessLists() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.allowFrom, p.allowGroupMembers
}

func (p *accessTestPlatform) SetAccessLists(allowFrom, allowGroupMembers string) error {
	p.mu.Lock()
	p.allowFrom = allowFrom
	p.allowGroupMembers = allowGroupMembers
	p.mu.Unlock()
	return nil
}

func TestResolveAccessValue(t *testing.T) {
	e := &Engine{}
	groupMsg := &Message{
		SessionKey: "feishu:oc_team:ou_owner",
		ChatType:   "group",
		Mentions:   []Mention{{UserID: "ou_member", Name: "Member"}},
	}
	p2pMsg := &Message{SessionKey: "feishu:oc_dm:ou_owner", ChatType: "p2p"}

	for _, tc := range []struct {
		name, key, current, raw, want string
		msg                           *Message
		wantErr                       bool
	}{
		{name: "this group", key: "allow_all_members", raw: "$this", msg: groupMsg, want: "oc_team"},
		{name: "this p2p rejected", key: "allow_all_members", raw: "$this", msg: p2pMsg, wantErr: true},
		{name: "mention", key: "allow_from", raw: "@_user_1", msg: groupMsg, want: "ou_member"},
		{name: "mention plus explicit", key: "allow_from", raw: "@_user_1,ou_two", msg: groupMsg, want: "ou_member,ou_two"},
		{name: "append", key: "allow_from", current: "ou_owner", raw: "+ou_member", want: "ou_owner,ou_member"},
		{name: "remove", key: "allow_from", current: "ou_owner,ou_member", raw: "-ou_member", want: "ou_owner"},
		{name: "remove all rejected", key: "allow_from", current: "ou_owner", raw: "-ou_owner", wantErr: true},
		{name: "none", key: "allow_all_members", current: "oc_team", raw: "none", want: ""},
		{name: "wildcard group rejected", key: "allow_all_members", raw: "*", wantErr: true},
		{name: "mention wrong key", key: "allow_all_members", raw: "@_user_1", msg: groupMsg, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.msg
			if msg == nil {
				msg = groupMsg
			}
			got, err := e.resolveAccessValue(msg, tc.key, tc.current, tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveAccessValue(%q) = %q, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveAccessValue: %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveAccessValue(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestCmdConfigAccess_SetAndGet(t *testing.T) {
	p := &accessTestPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}, allowFrom: "ou_owner"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{
		SessionKey: "feishu:oc_team:ou_owner",
		ChatType:   "group",
		Mentions:   []Mention{{UserID: "ou_member"}},
	}

	if handled := e.cmdConfigAccess(p, msg, []string{"set", "allow_all_members", "$this"}, "set"); !handled {
		t.Fatal("allow_all_members not handled")
	}
	if _, groups := p.AccessLists(); groups != "oc_team" {
		t.Fatalf("allow_all_members = %q, want oc_team", groups)
	}

	if handled := e.cmdConfigAccess(p, msg, []string{"set", "allow_from", "+@_user_1"}, "set"); !handled {
		t.Fatal("allow_from not handled")
	}
	allowFrom, _ := p.AccessLists()
	if allowFrom != "ou_owner,ou_member" {
		t.Fatalf("allow_from = %q, want owner+member", allowFrom)
	}

	if handled := e.cmdConfigAccess(p, msg, []string{"get", "allow_all_members"}, "get"); !handled {
		t.Fatal("allow_all_members get not handled")
	}
	sent := p.getSent()
	if len(sent) == 0 || !strings.Contains(sent[len(sent)-1], "oc_team") {
		t.Fatalf("get reply = %#v, want group id", sent)
	}
}

func TestConfigAccessKeysRequireAdmin(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"set", "allow_from", "ou_x"}, true},
		{[]string{"se", "allow_from", "ou_x"}, true},
		{[]string{"set", "allow_all_members", "$this"}, true},
		{[]string{"set", "reply_all_chats", "+$this"}, true},
		{[]string{"se", "reply_all_chats", "none"}, true},
		{[]string{"get", "reply_all_chats"}, false},
		{[]string{"set", "mode", "compact"}, false},
		{[]string{"get", "allow_from"}, false},
	} {
		if got := isPrivilegedCommandInvocation("config", tc.args); got != tc.want {
			t.Errorf("isPrivilegedCommandInvocation(config, %v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
