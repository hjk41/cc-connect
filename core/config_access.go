package core

import (
	"fmt"
	"strings"
)

// cmdConfigAccess handles the platform access keys exposed through /config.
// It returns true when the key belongs to this path, even if the operation
// failed, so the generic display-config path does not also handle it.
func (e *Engine) cmdConfigAccess(p Platform, msg *Message, args []string, sub string) bool {
	if len(args) < 2 {
		return false
	}
	key := strings.ToLower(args[1])
	if key != "allow_from" && key != "allow_all_members" {
		return false
	}
	updater, ok := p.(AccessListUpdater)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError,
			fmt.Errorf("runtime access grants are only supported on Feishu")))
		return true
	}
	allowFrom, allowGroupMembers := updater.AccessLists()

	if sub == "get" {
		value := allowFrom
		if key == "allow_all_members" {
			value = allowGroupMembers
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("`%s` = `%s`", key, value))
		return true
	}
	if sub != "set" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgConfigSetUsage))
		return true
	}
	if len(args) < 3 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgConfigSetUsage))
		return true
	}

	current := allowFrom
	if key == "allow_all_members" {
		current = allowGroupMembers
	}
	next, err := e.resolveAccessValue(msg, key, current, strings.Join(args[2:], " "))
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return true
	}
	if key == "allow_from" {
		if strings.TrimSpace(next) == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError,
				fmt.Errorf("allow_from cannot be empty: empty means allow all; use an explicit ID or '*'")))
			return true
		}
		allowFrom = next
	} else {
		allowGroupMembers = next
	}
	if err := updater.SetAccessLists(allowFrom, allowGroupMembers); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return true
	}
	allowFrom, allowGroupMembers = updater.AccessLists()
	if e.platformAccessSaveFunc != nil {
		if err := e.platformAccessSaveFunc(p.Name(), allowFrom, allowGroupMembers); err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError,
				fmt.Errorf("access updated at runtime, but saving config failed: %w", err)))
			return true
		}
	}
	updated := allowFrom
	if key == "allow_all_members" {
		updated = allowGroupMembers
	}
	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgConfigUpdated, key, updated))
	return true
}

func (e *Engine) resolveAccessValue(msg *Message, key, current, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	op := byte(0)
	switch {
	case strings.HasPrefix(raw, "+"):
		op = '+'
		raw = strings.TrimSpace(raw[1:])
	case strings.HasPrefix(raw, "-"):
		op = '-'
		raw = strings.TrimSpace(raw[1:])
	}
	if op == 0 && strings.EqualFold(raw, "none") {
		if key == "allow_from" {
			return "", fmt.Errorf("allow_from cannot be empty: empty means allow all")
		}
		return "", nil
	}
	values, err := e.resolveAccessTokens(msg, key, raw)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", fmt.Errorf("no access value provided")
	}
	next := strings.Join(values, ",")
	if op != 0 {
		base := splitAccessList(current)
		if op == '+' {
			base = append(base, values...)
		} else {
			base = removeAccessValues(base, values)
		}
		next = strings.Join(uniqueAccessValues(base), ",")
	}
	if key == "allow_from" && next == "" {
		return "", fmt.Errorf("allow_from cannot be empty: empty means allow all")
	}
	return next, nil
}

func (e *Engine) resolveAccessTokens(msg *Message, key, raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	var out []string
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		switch {
		case field == "$this":
			if key != "allow_all_members" {
				return nil, fmt.Errorf("$this is only valid for allow_all_members")
			}
			if msg.ChatType != "group" {
				return nil, fmt.Errorf("$this requires a group chat")
			}
			chatID := effectiveChannelID(msg)
			if chatID == "" {
				return nil, fmt.Errorf("cannot resolve the current group chat id")
			}
			out = append(out, chatID)
		case strings.HasPrefix(field, "@"):
			if key != "allow_from" {
				return nil, fmt.Errorf("@user is only valid for allow_from")
			}
			if len(msg.Mentions) == 0 {
				return nil, fmt.Errorf("cannot resolve @user; use the Feishu mention picker or an explicit ou_... id")
			}
			for _, mention := range msg.Mentions {
				if mention.UserID != "" {
					out = append(out, mention.UserID)
				}
			}
		default:
			if key == "allow_all_members" && field == "*" {
				return nil, fmt.Errorf("allow_all_members requires explicit group chat IDs, not '*'")
			}
			out = append(out, field)
		}
	}
	return uniqueAccessValues(out), nil
}

func splitAccessList(value string) []string {
	return uniqueAccessValues(strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}))
}

func uniqueAccessValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, value)
	}
	return out
}

func removeAccessValues(values, remove []string) []string {
	drop := make(map[string]bool, len(remove))
	for _, value := range remove {
		drop[strings.ToLower(strings.TrimSpace(value))] = true
	}
	out := values[:0]
	for _, value := range values {
		if !drop[strings.ToLower(strings.TrimSpace(value))] {
			out = append(out, value)
		}
	}
	return out
}
