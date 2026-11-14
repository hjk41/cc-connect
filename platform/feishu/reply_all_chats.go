package feishu

import (
	"fmt"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

var _ core.ReplyAllChatsUpdater = (*Platform)(nil)

func (p *Platform) ReplyAllChats() (chatIDs string, allGroups bool) {
	p.authMu.RLock()
	defer p.authMu.RUnlock()
	return p.replyAllChats, p.groupReplyAll
}

// SetReplyAllChats changes mention requirements only. User and chat grants
// remain independent; an empty list never means all chats.
func (p *Platform) SetReplyAllChats(chatIDs string) error {
	var ids []string
	seen := map[string]bool{}
	for _, id := range strings.FieldsFunc(chatIDs, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if !strings.HasPrefix(id, "oc_") || len(id) <= 3 || strings.ContainsAny(id, ":/$*") {
			return fmt.Errorf("%s: reply_all_chats requires explicit group chat IDs, got %q", p.Name(), id)
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	p.authMu.Lock()
	p.replyAllChats = strings.Join(ids, ",")
	p.authMu.Unlock()
	return nil
}

// repliesToAllInChat reports whether unmentioned group messages from chatID
// bypass the @mention gate. It combines the runtime-managed reply_all_chats
// list with the configured group_reply_all / group_reply_all_chats options;
// a non-empty group_reply_all_chats still overrides group_reply_all.
func (p *Platform) repliesToAllInChat(chatID string) bool {
	return p.inReplyAllChats(chatID) || p.groupReplyAllForChat(chatID)
}

// inReplyAllChats reports whether chatID is explicitly listed in
// reply_all_chats, independent of the group_reply_all switches.
func (p *Platform) inReplyAllChats(chatID string) bool {
	chats, _ := p.ReplyAllChats()
	for _, id := range strings.Split(chats, ",") {
		if id != "" && id == chatID {
			return true
		}
	}
	return false
}
