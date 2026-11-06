package feishu

import (
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

// isSenderAllowed keeps the user allowlist for private/unlisted chats and
// additionally admits any identified member of an explicitly configured group.
// groupChatID must come from the platform's verified group message/callback
// context, never from action values or message content. The existing mention
// and allow_chat filters still apply independently.
func (p *Platform) isSenderAllowed(userID, groupChatID string) bool {
	if userID == "" {
		return false
	}
	if core.AllowList(p.allowFrom, userID) {
		return true
	}
	if groupChatID == "" {
		return false
	}
	for _, chatID := range strings.Split(p.allowGroupMembers, ",") {
		if strings.EqualFold(strings.TrimSpace(chatID), groupChatID) {
			return true
		}
	}
	return false
}
