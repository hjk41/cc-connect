package dsh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenhg5/cc-connect/core"
	"github.com/klauspost/compress/zstd"
)

const titleRuneLimit = 60

type sessionMeta struct {
	ID         string
	Cwd        string
	CreatedAt  time.Time
	ModifiedAt time.Time
	Title      string
	History    []core.HistoryEntry
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type logLine struct {
	Type      string          `json:"type"`
	Time      int64           `json:"time"`
	ID        string          `json:"id"`
	Cwd       string          `json:"cwd"`
	CreatedAt int64           `json:"createdAt"`
	Data      json.RawMessage `json:"data"`
}

func parseSessionFile(path string) (sessionMeta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sessionMeta{}, err
	}
	if strings.HasSuffix(path, ".zstd") {
		dec, err := zstd.NewReader(nil)
		if err != nil {
			return sessionMeta{}, fmt.Errorf("dsh: zstd reader: %w", err)
		}
		defer dec.Close()
		raw, err = dec.DecodeAll(raw, nil)
		if err != nil {
			return sessionMeta{}, fmt.Errorf("dsh: decompress session log: %w", err)
		}
	}
	return parseSessionBytes(raw)
}

func parseSessionBytes(raw []byte) (sessionMeta, error) {
	var meta sessionMeta
	var firstUser string
	var sawTitle bool
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev logLine
		if err := json.Unmarshal(line, &ev); err != nil {
			return sessionMeta{}, fmt.Errorf("dsh: parse session log: %w", err)
		}
		when := msTime(ev.Time)
		if when.After(meta.ModifiedAt) {
			meta.ModifiedAt = when
		}
		switch ev.Type {
		case "session":
			meta.ID = strings.TrimSpace(ev.ID)
			meta.Cwd = strings.TrimSpace(ev.Cwd)
			if ev.CreatedAt > 0 {
				meta.CreatedAt = msTime(ev.CreatedAt)
			}
		case "session/title":
			title := titleFromData(ev.Data)
			if title != "" {
				meta.Title = title
				sawTitle = true
			}
		case "user/message":
			if text, ok := userMessageText(ev.Data); ok {
				appendHistory(&meta, "user", text, when)
				if firstUser == "" {
					firstUser = text
				}
			}
		case "agent/inbox/spliced":
			for _, text := range splicedUserTexts(ev.Data) {
				appendHistory(&meta, "user", text, when)
				if firstUser == "" {
					firstUser = text
				}
			}
		case "assistant/message":
			if text := assistantText(ev.Data); text != "" {
				appendHistory(&meta, "assistant", text, when)
			}
		}
	}
	if !sawTitle {
		meta.Title = summarizeTitle(firstUser)
	}
	if meta.ModifiedAt.IsZero() {
		meta.ModifiedAt = meta.CreatedAt
	}
	return meta, nil
}

func appendHistory(meta *sessionMeta, role, text string, when time.Time) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if n := len(meta.History); n > 0 {
		prev := meta.History[n-1]
		if prev.Role == role && prev.Content == text {
			return
		}
	}
	meta.History = append(meta.History, core.HistoryEntry{Role: role, Content: text, Timestamp: when})
}

func userMessageText(data json.RawMessage) (string, bool) {
	var body struct {
		Role   string `json:"role"`
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
		Content []contentBlock `json:"content"`
	}
	if json.Unmarshal(data, &body) != nil {
		return "", false
	}
	kind := strings.TrimSpace(body.Source.Kind)
	if kind != "" && kind != "user" {
		return "", false
	}
	if body.Role != "" && body.Role != "user" {
		return "", false
	}
	text := textBlocks(body.Content, false)
	if text == "" {
		return "", false
	}
	return text, true
}

func splicedUserTexts(data json.RawMessage) []string {
	var body struct {
		Inserted []struct {
			Role   string `json:"role"`
			Source struct {
				Kind string `json:"kind"`
			} `json:"source"`
			Content []contentBlock `json:"content"`
		} `json:"inserted"`
	}
	if json.Unmarshal(data, &body) != nil {
		return nil
	}
	var out []string
	for _, msg := range body.Inserted {
		if msg.Role != "" && msg.Role != "user" {
			continue
		}
		kind := strings.TrimSpace(msg.Source.Kind)
		if kind != "" && kind != "user" {
			continue
		}
		if text := textBlocks(msg.Content, false); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func assistantText(data json.RawMessage) string {
	var body struct {
		Message struct {
			Content []contentBlock `json:"content"`
		} `json:"message"`
		Content []contentBlock `json:"content"`
	}
	if json.Unmarshal(data, &body) != nil {
		return ""
	}
	blocks := body.Message.Content
	if len(blocks) == 0 {
		blocks = body.Content
	}
	return textBlocks(blocks, true)
}

func textBlocks(blocks []contentBlock, assistant bool) string {
	var parts []string
	for _, b := range blocks {
		kind := strings.TrimSpace(b.Type)
		if assistant && kind != "text" {
			continue
		}
		if !assistant && kind != "" && kind != "text" {
			continue
		}
		if text := strings.TrimSpace(b.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func titleFromData(data json.RawMessage) string {
	var body struct {
		Title string `json:"title"`
	}
	if json.Unmarshal(data, &body) != nil {
		return ""
	}
	return strings.TrimSpace(body.Title)
}

func summarizeTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	if utf8.RuneCountInString(text) <= titleRuneLimit {
		return text
	}
	runes := []rune(text)
	return string(runes[:titleRuneLimit])
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func pickSessionLog(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	bestScore := -1
	best := ""
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		score, ok := logScore(ent.Name())
		if !ok || score < bestScore {
			continue
		}
		bestScore = score
		best = filepath.Join(dir, ent.Name())
	}
	return best, best != ""
}

func logScore(name string) (int, bool) {
	zstdBonus := 0
	base := name
	if strings.HasSuffix(name, ".zstd") {
		zstdBonus = 1
		base = strings.TrimSuffix(name, ".zstd")
	}
	if !strings.HasSuffix(base, ".jsonl") {
		return 0, false
	}
	stem := strings.TrimSuffix(base, ".jsonl")
	version := 0
	switch {
	case stem == "session":
		version = 0
	case strings.HasPrefix(stem, "session.v"):
		n, ok := parseVersion(strings.TrimPrefix(stem, "session.v"))
		if !ok {
			return 0, false
		}
		version = n
	default:
		return 0, false
	}
	return version*2 + zstdBonus, true
}

func parseVersion(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
