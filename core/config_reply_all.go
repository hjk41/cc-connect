package core

import (
	"log/slog"
	"strings"
)

func (e *Engine) cmdConfigReplyAllChats(p Platform, msg *Message, args []string, sub string) bool {
	if len(args) < 2 || !strings.EqualFold(args[1], "reply_all_chats") {
		return false
	}
	updater, ok := p.(ReplyAllChatsUpdater)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgReplyAllChatsUnsupported))
		return true
	}
	// Serialize read/modify/write and persistence across concurrent admin commands.
	e.replyAllChatsConfigMu.Lock()
	defer e.replyAllChatsConfigMu.Unlock()
	current, allGroups := updater.ReplyAllChats()
	if sub == "get" {
		e.replyAllChatsResult(p, msg, current, allGroups)
		return true
	}
	if sub != "set" || len(args) < 3 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgReplyAllChatsUsage))
		return true
	}
	next, err := e.resolveAccessValue(msg, "reply_all_chats", current, strings.Join(args[2:], " "))
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgReplyAllChatsInvalid))
		return true
	}
	if err := updater.SetReplyAllChats(next); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgReplyAllChatsInvalid))
		return true
	}
	next, allGroups = updater.ReplyAllChats()
	if e.platformReplyAllChatsSaveFunc != nil {
		if err := e.platformReplyAllChatsSaveFunc(p.Name(), next); err != nil {
			// A failed save must not silently leave a non-durable change active.
			if rollbackErr := updater.SetReplyAllChats(current); rollbackErr != nil {
				slog.Error("config: restore reply-all chats", "error", rollbackErr)
			}
			slog.Error("config: save reply-all chats", "error", err)
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgReplyAllChatsSaveFailed))
			return true
		}
	}
	e.replyAllChatsResult(p, msg, next, allGroups)
	return true
}

func (e *Engine) replyAllChatsResult(p Platform, msg *Message, chats string, allGroups bool) {
	if chats == "" {
		chats = "none"
	}
	text := e.i18n.Tf(MsgConfigUpdated, "reply_all_chats", chats)
	if allGroups {
		text += "\n" + e.i18n.T(MsgReplyAllChatsGlobalEnabled)
	}
	e.reply(p, msg.ReplyCtx, text)
}
