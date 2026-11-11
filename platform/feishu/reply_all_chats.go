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

func (p *Platform) repliesToAllInChat(chatID string) bool {
	chats, allGroups := p.ReplyAllChats()
	if allGroups {
		return true
	}
	for _, id := range strings.Split(chats, ",") {
		if id != "" && id == chatID {
			return true
		}
	}
	return false
}
