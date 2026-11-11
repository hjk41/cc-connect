package core

import (
	"fmt"
	"strings"
)

// formatConfigMenu is shared by message commands and card navigation so both
// show the same live settings for the originating platform.
func (e *Engine) formatConfigMenu(p Platform) (body, hint string) {
	var sb strings.Builder
	sb.WriteString(e.i18n.T(MsgConfigTitle))
	writeItem := func(key, value string, desc MsgKey) {
		fmt.Fprintf(&sb, "`%s` = `%s`\n  %s\n\n", key, value, e.i18n.T(desc))
	}
	for _, item := range e.configItems() {
		writeItem(item.key, item.getFunc(), item.desc)
	}
	hint = e.i18n.T(MsgConfigHint)
	_, access := p.(AccessListUpdater)
	_, replyAll := p.(ReplyAllChatsUpdater)
	if access || replyAll {
		sb.WriteString(e.i18n.T(MsgConfigPlatformSettings) + "\n\n")
	}
	if updater, ok := p.(AccessListUpdater); ok {
		users, groups := updater.AccessLists()
		if strings.TrimSpace(users) == "" {
			users = "*"
		}
		writeItem("allow_from", users, MsgConfigAllowFrom)
		writeItem("allow_all_members", configListValue(groups), MsgConfigAllowAllMembers)
		hint += "\n\n" + e.i18n.T(MsgConfigAccessHint)
	}
	if updater, ok := p.(ReplyAllChatsUpdater); ok {
		chats, allGroups := updater.ReplyAllChats()
		writeItem("reply_all_chats", configListValue(chats), MsgConfigReplyAllChats)
		if allGroups {
			sb.WriteString(e.i18n.T(MsgReplyAllChatsGlobalEnabled) + "\n\n")
		}
		hint += "\n\n" + e.i18n.T(MsgReplyAllChatsUsage)
	}
	if e.configReloadFunc != nil {
		hint += "\n\n" + e.i18n.T(MsgConfigReloadHint)
	}
	return sb.String(), hint
}

func configListValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "none"
	}
	return value
}
