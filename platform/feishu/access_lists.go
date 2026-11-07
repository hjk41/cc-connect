package feishu

import (
	"fmt"
	"strings"
)

var _ interface {
	AccessLists() (string, string)
	SetAccessLists(string, string) error
} = (*Platform)(nil)

// AccessLists returns the current user and group-member grants.
func (p *Platform) AccessLists() (allowFrom, allowGroupMembers string) {
	p.authMu.RLock()
	defer p.authMu.RUnlock()
	return p.allowFrom, p.allowGroupMembers
}

// SetAccessLists replaces the runtime user and group-member grants. It does
// not touch allow_chat, which remains an independent chat-level filter.
func (p *Platform) SetAccessLists(allowFrom, allowGroupMembers string) error {
	allowFrom = strings.TrimSpace(allowFrom)
	allowGroupMembers = strings.TrimSpace(allowGroupMembers)
	for _, chatID := range strings.Split(allowGroupMembers, ",") {
		if strings.TrimSpace(chatID) == "*" {
			return fmt.Errorf("allow_all_members requires explicit group chat IDs, not '*'")
		}
	}
	p.authMu.Lock()
	p.allowFrom = allowFrom
	p.allowGroupMembers = allowGroupMembers
	p.authMu.Unlock()
	return nil
}
