package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	progressStyleLegacy  = "legacy"
	progressStyleCompact = "compact"
	progressStyleCard    = "card"

	// ProgressCardPayloadPrefix marks a structured payload for card-style progress.
	ProgressCardPayloadPrefix = "__cc_connect_progress_card_v1__:"

	// Keep a margin below platform hard limit for markdown wrappers/code fences.
	compactProgressMaxChars = maxPlatformMessageLen - 200

	// Bound each platform progress-card API call so a hung upstream request
	// does not block the whole turn forever.
	compactProgressAPITimeout = 15 * time.Second

	// progressDigestInterval is the longest a digest-mode batch waits before send.
	progressDigestInterval   = 30 * time.Second
	progressDigestFlushRunes = 3000
	progressMaxRollovers     = 2
)

var progressUpdateFailureBackoffs = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

type ProgressCardState string

const (
	ProgressCardStateRunning   ProgressCardState = "running"
	ProgressCardStateCompleted ProgressCardState = "completed"
	ProgressCardStateFailed    ProgressCardState = "failed"
)

type ProgressCardEntryKind string

const (
	ProgressEntryInfo       ProgressCardEntryKind = "info"
	ProgressEntryThinking   ProgressCardEntryKind = "thinking"
	ProgressEntryToolUse    ProgressCardEntryKind = "tool_use"
	ProgressEntryToolResult ProgressCardEntryKind = "tool_result"
	ProgressEntryError      ProgressCardEntryKind = "error"
)

type ProgressCardEntry struct {
	Kind     ProgressCardEntryKind `json:"kind"`
	Text     string                `json:"text"`
	Tool     string                `json:"tool,omitempty"`
	Status   string                `json:"status,omitempty"`
	ExitCode *int                  `json:"exit_code,omitempty"`
	Success  *bool                 `json:"success,omitempty"`
	// ToolCallID ties snapshots of one tool call. Replacement is in memory;
	// the id is not part of the platform payload.
	ToolCallID string `json:"-"`
	// Partial, for a tool result, means Text is a non-final output snapshot.
	// Digest mode skips these. Not part of the platform payload.
	Partial bool `json:"-"`
}

// ProgressCardPayload carries structured progress entries for platforms that
// render custom progress cards.
type ProgressCardPayload struct {
	Version   int                 `json:"version,omitempty"`
	Agent     string              `json:"agent,omitempty"`
	Lang      string              `json:"lang,omitempty"`
	State     ProgressCardState   `json:"state,omitempty"`
	Entries   []string            `json:"entries,omitempty"` // legacy fallback
	Items     []ProgressCardEntry `json:"items,omitempty"`   // ordered typed events
	Truncated bool                `json:"truncated"`
}

// BuildProgressCardPayload encodes progress entries into a transport string.
// This legacy builder keeps compatibility with old callers that only send text.
func BuildProgressCardPayload(entries []string, truncated bool) string {
	cleaned := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			cleaned = append(cleaned, entry)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	payload := ProgressCardPayload{
		Entries:   cleaned,
		Truncated: truncated,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return ProgressCardPayloadPrefix + string(b)
}

// BuildProgressCardPayloadV2 encodes ordered typed progress events.
func BuildProgressCardPayloadV2(items []ProgressCardEntry, truncated bool, agent string, lang Language, state ProgressCardState) string {
	cleaned := make([]ProgressCardEntry, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		kind := item.Kind
		if kind == "" {
			kind = ProgressEntryInfo
		}
		cleaned = append(cleaned, ProgressCardEntry{
			Kind:     kind,
			Text:     text,
			Tool:     strings.TrimSpace(item.Tool),
			Status:   strings.TrimSpace(item.Status),
			ExitCode: item.ExitCode,
			Success:  item.Success,
		})
	}
	if len(cleaned) == 0 {
		return ""
	}
	if state == "" {
		state = ProgressCardStateRunning
	}
	payload := ProgressCardPayload{
		Version:   2,
		Agent:     strings.TrimSpace(agent),
		Lang:      string(lang),
		State:     state,
		Items:     cleaned,
		Truncated: truncated,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return ProgressCardPayloadPrefix + string(b)
}

// ParseProgressCardPayload decodes a structured progress payload.
func ParseProgressCardPayload(content string) (*ProgressCardPayload, bool) {
	if !strings.HasPrefix(content, ProgressCardPayloadPrefix) {
		return nil, false
	}
	raw := strings.TrimPrefix(content, ProgressCardPayloadPrefix)
	var payload ProgressCardPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, false
	}
	legacy := make([]string, 0, len(payload.Entries))
	for _, entry := range payload.Entries {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			legacy = append(legacy, entry)
		}
	}
	items := make([]ProgressCardEntry, 0, len(payload.Items))
	for _, item := range payload.Items {
		item.Text = strings.TrimSpace(item.Text)
		item.Tool = strings.TrimSpace(item.Tool)
		item.Status = strings.TrimSpace(item.Status)
		if item.Text == "" {
			continue
		}
		if item.Kind == "" {
			item.Kind = ProgressEntryInfo
		}
		items = append(items, item)
	}
	if len(items) == 0 && len(legacy) > 0 {
		for _, entry := range legacy {
			items = append(items, ProgressCardEntry{
				Kind: inferLegacyEntryKind(entry),
				Text: entry,
			})
		}
	}
	if len(items) == 0 && len(legacy) == 0 {
		return nil, false
	}
	if payload.State == "" {
		payload.State = ProgressCardStateRunning
	}
	payload.Items = items
	payload.Entries = legacy
	if len(payload.Entries) == 0 && len(payload.Items) > 0 {
		payload.Entries = make([]string, 0, len(payload.Items))
		for _, item := range payload.Items {
			payload.Entries = append(payload.Entries, item.Text)
		}
	}
	return &payload, true
}

func inferLegacyEntryKind(entry string) ProgressCardEntryKind {
	switch {
	case strings.HasPrefix(entry, "💭"):
		return ProgressEntryThinking
	case strings.HasPrefix(entry, "🔧"), strings.Contains(entry, "**Tool #"):
		return ProgressEntryToolUse
	case strings.HasPrefix(entry, "🧾"):
		return ProgressEntryToolResult
	case strings.HasPrefix(entry, "❌"):
		return ProgressEntryError
	default:
		return ProgressEntryInfo
	}
}

// compactProgressWriter coalesces intermediate progress (thinking/tool-use)
// into one editable message for platforms that support message updates.
// A timer goroutine may flush a trailing edit, so every field read or written
// after construction is guarded by mu.
type compactProgressWriter struct {
	mu        sync.Mutex
	ctx       context.Context
	platform  Platform
	replyCtx  any
	transform func(string) string

	starter PreviewStarter
	updater MessageUpdater
	handle  any

	enabled    bool
	style      string
	usePayload bool

	content string
	entries []string
	items   []ProgressCardEntry
	// entryCallIDs is parallel to entries. A non-empty id marks a tool-result
	// line that a later snapshot with the same id replaces.
	entryCallIDs []string
	state        ProgressCardState
	agentName    string
	lang         Language
	truncated    bool
	lastSent     string
	maxEntries   int

	// minUpdateInterval is the gap between progress edits. The larger of the
	// core interval and a platform ProgressUpdateThrottler wins.
	minUpdateInterval time.Duration
	lastUpdateAt      time.Time
	backoffUntil      time.Time

	consecutiveFailures int
	rollovers           int
	maxRollovers        int
	createAttempts      int
	createRetryDelay    time.Duration
	updateBackoffs      []time.Duration

	digestMode       bool
	digestHeader     string
	digestBuf        strings.Builder
	digestSeeded     int
	digestInterval   time.Duration
	digestFlushRunes int

	finalized bool

	now       func() time.Time
	afterFunc func(time.Duration, func()) func() bool
	timerStop func() bool
	timerGen  int
}

func normalizeProgressStyle(style string) string {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "", progressStyleLegacy:
		return progressStyleLegacy
	case progressStyleCompact:
		return progressStyleCompact
	case progressStyleCard:
		return progressStyleCard
	default:
		return progressStyleLegacy
	}
}

func progressStyleForPlatform(p Platform) string {
	ps := progressStyleLegacy
	if sp, ok := p.(ProgressStyleProvider); ok {
		ps = normalizeProgressStyle(sp.ProgressStyle())
	}
	return ps
}

type progressStyleHintProvider interface {
	progressStyleHint() string
}

type progressCardPayloadHintProvider interface {
	supportsProgressCardPayloadHint() bool
}

func progressStyleForTarget(p Platform, replyCtx any) string {
	if hint, ok := replyCtx.(progressStyleHintProvider); ok {
		return normalizeProgressStyle(hint.progressStyleHint())
	}
	return progressStyleForPlatform(p)
}

func progressCardPayloadForTarget(p Platform, replyCtx any) bool {
	if hint, ok := replyCtx.(progressCardPayloadHintProvider); ok {
		return hint.supportsProgressCardPayloadHint()
	}
	if cap, ok := p.(ProgressCardPayloadSupport); ok {
		return cap.SupportsProgressCardPayload()
	}
	return false
}

// SuppressStandaloneToolResultEvent is true when a platform opts into progress
// styling (ProgressStyleProvider) but uses legacy mode. In that case tool_use
// lines are still shown, but a separate chat message for EventToolResult is
// skipped to avoid duplicate noise (e.g. Codex structured tool results on Feishu).
// Platforms without ProgressStyleProvider keep showing standalone tool results.
func SuppressStandaloneToolResultEvent(p Platform) bool {
	_, ok := p.(ProgressStyleProvider)
	if !ok {
		return false
	}
	return progressStyleForPlatform(p) == progressStyleLegacy
}

func progressEditInterval(coreInterval time.Duration, p Platform) time.Duration {
	if coreInterval < 0 {
		coreInterval = 0
	}
	interval := coreInterval
	if throttler, ok := p.(ProgressUpdateThrottler); ok {
		if pi := throttler.ProgressUpdateInterval(); pi > interval {
			interval = pi
		}
	}
	return interval
}

func newCompactProgressWriter(ctx context.Context, p Platform, replyCtx any, agentName string, lang Language, transform func(string) string, coreInterval time.Duration, digestHeader string) *compactProgressWriter {
	w := &compactProgressWriter{
		ctx:              ctx,
		platform:         p,
		replyCtx:         replyCtx,
		transform:        transform,
		style:            progressStyleForTarget(p, replyCtx),
		state:            ProgressCardStateRunning,
		agentName:        normalizeProgressAgentLabel(agentName),
		lang:             lang,
		maxEntries:       10,
		maxRollovers:     progressMaxRollovers,
		createRetryDelay: 2 * time.Second,
		updateBackoffs:   append([]time.Duration(nil), progressUpdateFailureBackoffs...),
		digestHeader:     strings.TrimSpace(digestHeader),
		digestInterval:   progressDigestInterval,
		digestFlushRunes: progressDigestFlushRunes,
		now:              time.Now,
		afterFunc: func(d time.Duration, f func()) func() bool {
			t := time.AfterFunc(d, f)
			return t.Stop
		},
	}
	w.minUpdateInterval = progressEditInterval(coreInterval, p)
	if w.style != progressStyleCompact && w.style != progressStyleCard {
		slog.Debug("progress writer disabled: unsupported style", "platform", p.Name(), "style", w.style)
		return w
	}
	updater, ok := p.(MessageUpdater)
	if !ok {
		slog.Debug("progress writer disabled: platform has no MessageUpdater", "platform", p.Name(), "style", w.style)
		return w
	}
	w.enabled = true
	w.updater = updater
	if starter, ok := p.(PreviewStarter); ok {
		w.starter = starter
	}
	if w.style == progressStyleCard {
		if progressCardPayloadForTarget(p, replyCtx) {
			w.usePayload = true
		}
	}
	slog.Debug("progress writer enabled", "platform", p.Name(), "style", w.style, "use_payload", w.usePayload)
	return w
}

// userVisibleAgentName is the label shown on progress cards and session
// lists. Agents that implement AgentDoctorInfo supply it (for example a
// product name that is not the config type). Otherwise the progress alias
// table, then a capitalized type name, is used.
func userVisibleAgentName(agent Agent) string {
	if agent == nil {
		return "Agent"
	}
	if info, ok := agent.(AgentDoctorInfo); ok {
		if label := strings.TrimSpace(info.CLIDisplayName()); label != "" {
			return label
		}
	}
	return normalizeProgressAgentLabel(agent.Name())
}

func normalizeProgressAgentLabel(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "agent":
		return "Agent"
	case "codex":
		return "Codex"
	case "claudecode", "claude-code", "cc":
		return "CC"
	case "gemini":
		return "Gemini"
	case "cursor":
		return "Cursor"
	case "qoder":
		return "Qoder"
	case "iflow":
		return "iFlow"
	case "opencode":
		return "OpenCode"
	case "pi":
		return "PI"
	default:
		n := strings.TrimSpace(name)
		if n == "" {
			return "Agent"
		}
		return strings.ToUpper(n[:1]) + n[1:]
	}
}

// Append appends one progress item and updates the in-place message.
// Returns true when compact rendering handled this item; false means caller
// should fallback to legacy per-event send.
func (w *compactProgressWriter) Append(item string) bool {
	return w.AppendEvent(ProgressEntryInfo, item, "", item)
}

// AppendEvent appends one typed progress event and updates the in-place message.
// fallback is used for compact/plain rendering when style-specific rendering is not available.
func (w *compactProgressWriter) AppendEvent(kind ProgressCardEntryKind, text string, tool string, fallback string) bool {
	return w.AppendStructured(ProgressCardEntry{
		Kind: kind,
		Text: text,
		Tool: tool,
	}, fallback)
}

// AppendStructured appends one structured progress event and updates the in-place message.
// It returns false only when the writer is disabled (legacy style, or the platform
// cannot edit messages). Transient card failures stay inside the writer.
func (w *compactProgressWriter) AppendStructured(item ProgressCardEntry, fallback string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled || w.finalized {
		return w.enabled && w.finalized
	}
	if w.ctx.Err() != nil {
		return true
	}
	text := strings.TrimSpace(item.Text)
	fallback = strings.TrimSpace(fallback)
	if text == "" && fallback == "" {
		return true
	}
	if text == "" {
		text = fallback
	}
	if fallback == "" {
		fallback = text
	}
	switch item.Kind {
	case ProgressEntryThinking, ProgressEntryError, ProgressEntryInfo:
		if w.transform != nil {
			text = w.transform(text)
			fallback = w.transform(fallback)
		}
	}
	kind := item.Kind
	if kind == "" {
		kind = ProgressEntryInfo
	}
	item.Kind = kind
	item.Text = text
	item.Tool = strings.TrimSpace(item.Tool)
	item.Status = strings.TrimSpace(item.Status)
	item.ToolCallID = strings.TrimSpace(item.ToolCallID)
	if item.Kind != ProgressEntryToolResult {
		item.ToolCallID = ""
		item.Partial = false
	}

	// Digest messages are append-only. A non-final snapshot would just be
	// replaced later, so it never becomes its own chat line.
	if w.digestMode && item.Kind == ProgressEntryToolResult && item.Partial {
		return true
	}

	callID := item.ToolCallID
	replaced := false
	if callID != "" && !w.digestMode {
		if idx := w.toolResultIndexLocked(callID); idx >= 0 {
			if idx < len(w.items) {
				w.items[idx] = item
			}
			if idx < len(w.entries) {
				w.entries[idx] = fallback
			}
			w.entryCallIDs[idx] = callID
			replaced = true
		}
	}
	if !replaced {
		switch w.style {
		case progressStyleCard:
			w.items = append(w.items, item)
			w.entries = append(w.entries, fallback)
			w.entryCallIDs = append(w.entryCallIDs, callID)
			truncated := false
			if w.maxEntries > 0 && len(w.items) > w.maxEntries {
				drop := len(w.items) - w.maxEntries
				w.items = w.items[drop:]
				if len(w.entries) > w.maxEntries {
					w.entries = w.entries[len(w.entries)-w.maxEntries:]
				}
				if len(w.entryCallIDs) > w.maxEntries {
					w.entryCallIDs = w.entryCallIDs[len(w.entryCallIDs)-w.maxEntries:]
				}
				w.digestSeeded -= drop
				if w.digestSeeded < 0 {
					w.digestSeeded = 0
				}
				truncated = true
			}
			w.truncated = truncated
		default:
			w.entries = append(w.entries, fallback)
			w.entryCallIDs = append(w.entryCallIDs, callID)
			if w.content == "" {
				w.content = fallback
			} else {
				w.content += "\n\n" + fallback
			}
			w.content = trimCompactProgressText(w.content, compactProgressMaxChars)
		}
	}

	switch w.style {
	case progressStyleCard:
		if !w.renderCardContentLocked() {
			return true
		}
	default:
		if replaced {
			w.content = strings.Join(w.entries, "\n\n")
			w.content = trimCompactProgressText(w.content, compactProgressMaxChars)
		}
	}

	if w.digestMode {
		w.consumeNewDigestsLocked()
		w.maybeFlushDigestLocked(false)
		return true
	}
	w.syncLocked(false)
	return true
}

// toolResultIndexLocked returns the entries index of a tool result with callID.
func (w *compactProgressWriter) toolResultIndexLocked(callID string) int {
	for i, id := range w.entryCallIDs {
		if id == callID {
			return i
		}
	}
	return -1
}

// renderCardContentLocked refreshes w.content from the current items.
// It returns false when payload encoding fails and the writer has switched to digest mode.
func (w *compactProgressWriter) renderCardContentLocked() bool {
	if w.usePayload {
		w.content = BuildProgressCardPayloadV2(w.items, w.truncated, w.agentName, w.lang, w.state)
		if w.content == "" {
			slog.Warn("progress writer: failed to build structured payload", "platform", w.platform.Name())
			w.enterDigestLocked()
			w.consumeNewDigestsLocked()
			w.maybeFlushDigestLocked(false)
			return false
		}
		return true
	}
	w.content = renderCardProgressMarkdownFallback(w.entries, w.truncated)
	w.content = trimCompactProgressText(w.content, compactProgressMaxChars)
	return true
}

// Finalize pushes the terminal card state, or flushes a digest. It is safe to call twice.
func (w *compactProgressWriter) Finalize(state ProgressCardState) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled {
		return false
	}
	if w.finalized {
		return true
	}
	w.finalized = true
	w.cancelTimerLocked()
	if w.ctx.Err() != nil {
		return true
	}
	if w.digestMode {
		w.flushDigestLocked()
		return true
	}
	if state == "" {
		state = ProgressCardStateCompleted
	}
	if w.style == progressStyleCard && w.usePayload && (len(w.items) > 0 || w.content != "") {
		w.state = state
		rebuilt := BuildProgressCardPayloadV2(w.items, w.truncated, w.agentName, w.lang, w.state)
		if rebuilt == "" {
			slog.Warn("progress writer: failed to build final payload", "platform", w.platform.Name())
			w.enterDigestLocked()
			w.flushDigestLocked()
			return true
		}
		w.content = rebuilt
	}
	if w.content == "" {
		return true
	}
	if w.handle == nil {
		w.createAttempts = 0
		w.tryCreateLocked()
		if w.digestMode {
			w.flushDigestLocked()
		}
		return true
	}
	if w.content == w.lastSent {
		return true
	}
	if err := w.updateOnceLocked(); err != nil {
		if w.ctx.Err() != nil {
			return true
		}
		slog.Warn("progress writer: Finalize UpdateMessage failed, retrying", "platform", w.platform.Name(), "error", err)
		if err2 := w.updateOnceLocked(); err2 != nil {
			if w.ctx.Err() != nil {
				return true
			}
			slog.Warn("progress writer: Finalize UpdateMessage failed", "platform", w.platform.Name(), "error", err2)
			return false
		}
	}
	w.lastSent = w.content
	w.lastUpdateAt = w.now()
	w.consecutiveFailures = 0
	return true
}

func (w *compactProgressWriter) syncLocked(force bool) {
	if w.ctx.Err() != nil || w.digestMode || w.finalized {
		return
	}
	if force {
		w.backoffUntil = time.Time{}
	} else if !w.backoffUntil.IsZero() && w.now().Before(w.backoffUntil) {
		return
	}
	if w.content == "" || w.content == w.lastSent {
		return
	}
	if w.handle == nil {
		w.tryCreateLocked()
		return
	}
	if !force && w.minUpdateInterval > 0 {
		elapsed := w.now().Sub(w.lastUpdateAt)
		if elapsed < w.minUpdateInterval {
			w.scheduleLocked(w.minUpdateInterval-elapsed, false)
			return
		}
	}
	w.tryUpdateLocked()
}

func (w *compactProgressWriter) tryCreateLocked() {
	if w.ctx.Err() != nil {
		return
	}
	handle, err := w.createOnceLocked()
	if err == nil && w.ctx.Err() == nil {
		w.handle = handle
		w.lastSent = w.content
		w.lastUpdateAt = w.now()
		w.consecutiveFailures = 0
		w.createAttempts = 0
		w.backoffUntil = time.Time{}
		return
	}
	if w.ctx.Err() != nil {
		return
	}
	slog.Warn("progress writer: create failed", "platform", w.platform.Name(), "style", w.style, "error", err)
	w.createAttempts++
	if w.createAttempts <= 1 {
		w.backoffUntil = w.now().Add(w.createRetryDelay)
		w.scheduleLocked(w.createRetryDelay, true)
		return
	}
	w.enterDigestLocked()
	w.maybeFlushDigestLocked(false)
}

func (w *compactProgressWriter) createOnceLocked() (any, error) {
	if w.starter != nil {
		callCtx, cancel := w.withAPITimeout()
		handle, err := w.starter.SendPreviewStart(callCtx, w.replyCtx, w.content)
		cancel()
		if err != nil {
			return nil, err
		}
		if handle == nil {
			return nil, fmt.Errorf("progress writer: empty preview handle")
		}
		return handle, nil
	}
	callCtx, cancel := w.withAPITimeout()
	err := w.platform.Send(callCtx, w.replyCtx, w.content)
	cancel()
	if err != nil {
		return nil, err
	}
	return w.replyCtx, nil
}

func (w *compactProgressWriter) tryUpdateLocked() {
	err := w.updateOnceLocked()
	if err == nil {
		w.lastSent = w.content
		w.lastUpdateAt = w.now()
		w.consecutiveFailures = 0
		w.backoffUntil = time.Time{}
		return
	}
	if w.ctx.Err() != nil {
		return
	}
	slog.Warn("progress writer: UpdateMessage failed", "platform", w.platform.Name(), "style", w.style, "error", err)
	w.consecutiveFailures++
	if w.consecutiveFailures >= 3 {
		w.rolloverLocked()
		return
	}
	delay := w.backoffDelay(w.consecutiveFailures)
	w.backoffUntil = w.now().Add(delay)
	w.scheduleLocked(delay, true)
}

func (w *compactProgressWriter) updateOnceLocked() error {
	callCtx, cancel := w.withAPITimeout()
	err := w.updater.UpdateMessage(callCtx, w.handle, w.content)
	cancel()
	return err
}

func (w *compactProgressWriter) backoffDelay(failures int) time.Duration {
	backoffs := w.updateBackoffs
	if len(backoffs) == 0 {
		backoffs = progressUpdateFailureBackoffs
	}
	idx := failures - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(backoffs) {
		idx = len(backoffs) - 1
	}
	return backoffs[idx]
}

func (w *compactProgressWriter) rolloverLocked() {
	if w.ctx.Err() != nil {
		return
	}
	w.handle = nil
	w.lastSent = ""
	w.consecutiveFailures = 0
	w.backoffUntil = time.Time{}
	w.cancelTimerLocked()
	if w.rollovers >= w.maxRollovers {
		w.enterDigestLocked()
		w.flushDigestLocked()
		return
	}
	w.rollovers++
	w.createAttempts = 0
	w.tryCreateLocked()
}

func (w *compactProgressWriter) enterDigestLocked() {
	if w.digestMode {
		return
	}
	w.digestMode = true
	w.handle = nil
	w.cancelTimerLocked()
	w.backoffUntil = time.Time{}
	w.consumeNewDigestsLocked()
}

func (w *compactProgressWriter) consumeNewDigestsLocked() {
	for w.digestSeeded < len(w.entries) {
		w.appendDigestLocked(w.entries[w.digestSeeded])
		w.digestSeeded++
	}
}

func (w *compactProgressWriter) appendDigestLocked(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if w.digestBuf.Len() > 0 {
		w.digestBuf.WriteByte('\n')
	}
	w.digestBuf.WriteString(text)
}

func (w *compactProgressWriter) maybeFlushDigestLocked(force bool) {
	if w.ctx.Err() != nil || w.digestBuf.Len() == 0 {
		return
	}
	if !force && utf8.RuneCountInString(w.digestBuf.String()) < w.digestFlushRunes {
		w.scheduleLocked(w.digestInterval, false)
		return
	}
	w.flushDigestLocked()
}

func (w *compactProgressWriter) flushDigestLocked() {
	if w.ctx.Err() != nil {
		return
	}
	body := strings.TrimSpace(w.digestBuf.String())
	w.digestBuf.Reset()
	if body == "" {
		return
	}
	header := w.digestHeader
	if header == "" {
		header = "Progress (live card unavailable)"
	}
	budget := maxPlatformMessageLen - utf8.RuneCountInString(header) - 1
	if budget < 1 {
		budget = maxPlatformMessageLen
	}
	chunks := SplitMessageCodeFenceAware(body, budget)
	for _, chunk := range chunks {
		msg := header + "\n" + chunk
		callCtx, cancel := w.withAPITimeout()
		err := w.platform.Send(callCtx, w.replyCtx, msg)
		cancel()
		if err != nil {
			slog.Warn("progress writer: digest send failed", "platform", w.platform.Name(), "error", err)
			return
		}
	}
}

func (w *compactProgressWriter) scheduleLocked(delay time.Duration, replace bool) {
	if w.timerStop != nil && !replace {
		return
	}
	if delay < 0 {
		delay = 0
	}
	w.cancelTimerLocked()
	gen := w.timerGen
	w.timerStop = w.afterFunc(delay, func() { w.onTimer(gen) })
}

func (w *compactProgressWriter) cancelTimerLocked() {
	w.timerGen++
	if w.timerStop != nil {
		w.timerStop()
		w.timerStop = nil
	}
}

func (w *compactProgressWriter) onTimer(gen int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if gen != w.timerGen || w.finalized {
		return
	}
	w.timerStop = nil
	if w.ctx.Err() != nil {
		return
	}
	if w.digestMode {
		w.flushDigestLocked()
		return
	}
	w.syncLocked(true)
}

func (w *compactProgressWriter) withAPITimeout() (context.Context, context.CancelFunc) {
	if w.ctx.Err() != nil {
		return w.ctx, func() {}
	}
	if _, hasDeadline := w.ctx.Deadline(); hasDeadline {
		return w.ctx, func() {}
	}
	return context.WithTimeout(w.ctx, compactProgressAPITimeout)
}

func renderCardProgressMarkdownFallback(entries []string, truncated bool) string {
	var b strings.Builder
	b.WriteString("⏳ **Progress**\n")
	if truncated {
		b.WriteString("_Showing latest updates only._\n")
	}
	for i, entry := range entries {
		b.WriteString("\n")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(strings.ReplaceAll(entry, "\n", "\n   "))
	}
	return b.String()
}

func trimCompactProgressText(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	s = strings.TrimPrefix(s, "…\n")
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	rs := []rune(s)
	tail := strings.TrimLeft(string(rs[len(rs)-maxRunes:]), "\n")
	return "…\n" + tail
}
