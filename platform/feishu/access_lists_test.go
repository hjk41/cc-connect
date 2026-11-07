package feishu

import (
	"sync"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestSetAccessListsUpdatesRuntimeGrants(t *testing.T) {
	p := &Platform{allowFrom: "ou_owner", allowChat: "oc_allowed"}
	if err := p.SetAccessLists("ou_owner", "oc_team"); err != nil {
		t.Fatal(err)
	}
	allowFrom, groups := p.AccessLists()
	if allowFrom != "ou_owner" || groups != "oc_team" {
		t.Fatalf("grants = %q/%q", allowFrom, groups)
	}
	if p.allowChat != "oc_allowed" {
		t.Fatalf("allow_chat changed to %q", p.allowChat)
	}
	if !p.isSenderAllowed("ou_member", "oc_team") {
		t.Fatal("group member not allowed")
	}
	if p.isSenderAllowed("ou_member", "oc_other") {
		t.Fatal("group grant leaked to another group")
	}
	if p.isSenderAllowed("ou_member", "") {
		t.Fatal("group grant leaked to private chat")
	}
}

func TestSetAccessListsRejectsWildcard(t *testing.T) {
	p := &Platform{allowFrom: "ou_owner", allowGroupMembers: "oc_team"}
	if err := p.SetAccessLists("ou_new", "*"); err == nil {
		t.Fatal("wildcard group grant accepted")
	}
	allowFrom, groups := p.AccessLists()
	if allowFrom != "ou_owner" || groups != "oc_team" {
		t.Fatalf("failed update mutated state: %q/%q", allowFrom, groups)
	}
}

func TestAccessListsConcurrentReadWrite(t *testing.T) {
	p := &Platform{allowFrom: "ou_owner", allowGroupMembers: "oc_team"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p.AccessLists()
				_ = p.isSenderAllowed("ou_member", "oc_team")
				_ = p.SetAccessLists("ou_owner,ou_member", "oc_team")
			}
		}()
	}
	wg.Wait()
}

func TestCoreMentionsFiltersBotAndEmptyIDs(t *testing.T) {
	bot := "ou_bot"
	member := "ou_member"
	name := "Member"
	p := &Platform{botOpenID: bot}
	got := p.coreMentions([]*larkim.MentionEvent{
		{Id: &larkim.UserId{OpenId: &bot}, Name: &name},
		{Id: &larkim.UserId{OpenId: &member}, Name: &name},
		{Id: &larkim.UserId{}},
		nil,
	})
	if len(got) != 1 || got[0].UserID != member || got[0].Name != name {
		t.Fatalf("coreMentions = %#v", got)
	}
}
