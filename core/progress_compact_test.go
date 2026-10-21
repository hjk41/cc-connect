package core

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type suppressTestPlatform struct {
	style string
}

func (s *suppressTestPlatform) Name() string                             { return "test" }
func (s *suppressTestPlatform) Start(MessageHandler) error               { return nil }
func (s *suppressTestPlatform) Reply(context.Context, any, string) error { return nil }
func (s *suppressTestPlatform) Send(context.Context, any, string) error  { return nil }
func (s *suppressTestPlatform) Stop() error                              { return nil }
func (s *suppressTestPlatform) ProgressStyle() string                    { return s.style }

type displayNameAgent struct {
	stubAgent
	label string
}

func (a *displayNameAgent) Name() string           { return "dsh" }
func (a *displayNameAgent) CLIBinaryName() string  { return "dsh" }
func (a *displayNameAgent) CLIDisplayName() string { return a.label }

func TestUserVisibleAgentName_UsesDisplayName(t *testing.T) {
	got := userVisibleAgentName(&displayNameAgent{label: "DeepSeek Harness"})
	if got != "DeepSeek Harness" {
		t.Fatalf("label = %q", got)
	}
	if got := normalizeProgressAgentLabel("dsh"); got != "Dsh" {
		t.Fatalf("type name label = %q, want Dsh", got)
	}
	if got := normalizeProgressAgentLabel("DeepSeek Harness"); got != "DeepSeek Harness" {
		t.Fatalf("display label = %q", got)
	}
	if got := userVisibleAgentName(&stubAgent{}); got != "Stub" {
		t.Fatalf("fallback = %q, want Stub", got)
	}
}

func TestCompactProgressWriter_KeepsDisplayName(t *testing.T) {
	p := &previewCapturePlatform{}
	replyCtx := progressHintReplyCtx{style: progressStyleCard, payload: true}
	w := newCompactProgressWriter(context.Background(), p, replyCtx, userVisibleAgentName(&displayNameAgent{label: "DeepSeek Harness"}), LangChinese, nil, 0, "")
	if !w.AppendEvent(ProgressEntryInfo, "done", "", "done") {
		t.Fatal("append failed")
	}
	if !w.Finalize(ProgressCardStateCompleted) {
		t.Fatal("finalize failed")
	}
	parsed, ok := ParseProgressCardPayload(p.updated[len(p.updated)-1])
	if !ok {
		t.Fatal("payload did not parse")
	}
	if parsed.Agent != "DeepSeek Harness" {
		t.Fatalf("agent = %q", parsed.Agent)
	}
}

func TestSuppressStandaloneToolResultEvent(t *testing.T) {
	if SuppressStandaloneToolResultEvent(&stubPlatformNoProgress{}) {
		t.Fatal("platform without ProgressStyleProvider should not suppress")
	}
	if !SuppressStandaloneToolResultEvent(&suppressTestPlatform{style: "legacy"}) {
		t.Fatal("legacy ProgressStyleProvider should suppress standalone tool results")
	}
	if SuppressStandaloneToolResultEvent(&suppressTestPlatform{style: "compact"}) {
		t.Fatal("compact should not suppress (writer absorbs tool results)")
	}
	if SuppressStandaloneToolResultEvent(&suppressTestPlatform{style: "card"}) {
		t.Fatal("card should not suppress")
	}
}

// stubPlatformNoProgress is a minimal Platform without ProgressStyleProvider.
type stubPlatformNoProgress struct{}

func (stubPlatformNoProgress) Name() string                             { return "plain" }
func (stubPlatformNoProgress) Start(MessageHandler) error               { return nil }
func (stubPlatformNoProgress) Reply(context.Context, any, string) error { return nil }
func (stubPlatformNoProgress) Send(context.Context, any, string) error  { return nil }
func (stubPlatformNoProgress) Stop() error                              { return nil }

type progressHintReplyCtx struct {
	style   string
	payload bool
}

func (r progressHintReplyCtx) progressStyleHint() string { return r.style }

func (r progressHintReplyCtx) supportsProgressCardPayloadHint() bool { return r.payload }

type previewCapturePlatform struct {
	started []string
	updated []string
}

func (p *previewCapturePlatform) Name() string                             { return "bridge" }
func (p *previewCapturePlatform) Start(MessageHandler) error               { return nil }
func (p *previewCapturePlatform) Reply(context.Context, any, string) error { return nil }
func (p *previewCapturePlatform) Send(context.Context, any, string) error  { return nil }
func (p *previewCapturePlatform) Stop() error                              { return nil }

func (p *previewCapturePlatform) SendPreviewStart(_ context.Context, _ any, content string) (any, error) {
	p.started = append(p.started, content)
	return "preview-1", nil
}

func (p *previewCapturePlatform) UpdateMessage(_ context.Context, _ any, content string) error {
	p.updated = append(p.updated, content)
	return nil
}

func TestBuildAndParseProgressCardPayload(t *testing.T) {
	payload := BuildProgressCardPayload([]string{" step1 ", "", "step2"}, true)
	if payload == "" {
		t.Fatal("BuildProgressCardPayload returned empty string")
	}
	if !strings.HasPrefix(payload, ProgressCardPayloadPrefix) {
		t.Fatalf("payload = %q, want prefix %q", payload, ProgressCardPayloadPrefix)
	}

	parsed, ok := ParseProgressCardPayload(payload)
	if !ok {
		t.Fatalf("ParseProgressCardPayload should succeed, payload=%q", payload)
	}
	if len(parsed.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(parsed.Entries))
	}
	if parsed.Entries[0] != "step1" || parsed.Entries[1] != "step2" {
		t.Fatalf("entries = %#v, want [step1 step2]", parsed.Entries)
	}
	if !parsed.Truncated {
		t.Fatal("parsed.Truncated = false, want true")
	}
	if len(parsed.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(parsed.Items))
	}
	if parsed.Items[0].Kind != ProgressEntryInfo || parsed.Items[0].Text != "step1" {
		t.Fatalf("items[0] = %#v, want info/step1", parsed.Items[0])
	}
}

func TestCompactProgressWriter_UsesReplyContextHints(t *testing.T) {
	p := &previewCapturePlatform{}
	replyCtx := progressHintReplyCtx{
		style:   progressStyleCard,
		payload: true,
	}

	w := newCompactProgressWriter(context.Background(), p, replyCtx, "codex", LangEnglish, nil, 0, "")
	if !w.enabled {
		t.Fatal("progress writer should be enabled")
	}
	if !w.usePayload {
		t.Fatal("progress writer should use payload when reply context advertises it")
	}
	if got := w.style; got != progressStyleCard {
		t.Fatalf("style = %q, want %q", got, progressStyleCard)
	}

	if !w.AppendEvent(ProgressEntryThinking, "planning bridge progress", "", "planning bridge progress") {
		t.Fatal("AppendEvent() = false, want true")
	}
	if len(p.started) != 1 {
		t.Fatalf("started = %d, want 1", len(p.started))
	}
	if !strings.HasPrefix(p.started[0], ProgressCardPayloadPrefix) {
		t.Fatalf("preview start payload = %q, want progress payload prefix", p.started[0])
	}

	if !w.Finalize(ProgressCardStateCompleted) {
		t.Fatal("Finalize() = false, want true")
	}
	if len(p.updated) != 1 {
		t.Fatalf("updated = %d, want 1", len(p.updated))
	}

	parsed, ok := ParseProgressCardPayload(p.updated[0])
	if !ok {
		t.Fatalf("ParseProgressCardPayload() failed for %q", p.updated[0])
	}
	if parsed.State != ProgressCardStateCompleted {
		t.Fatalf("state = %q, want %q", parsed.State, ProgressCardStateCompleted)
	}
}

func TestBuildAndParseProgressCardPayloadV2(t *testing.T) {
	payload := BuildProgressCardPayloadV2([]ProgressCardEntry{
		{Kind: ProgressEntryThinking, Text: " plan "},
		{Kind: ProgressEntryToolUse, Tool: "Bash", Text: "pwd"},
	}, false, "Codex", LangChinese, ProgressCardStateRunning)
	if payload == "" {
		t.Fatal("BuildProgressCardPayloadV2 returned empty string")
	}

	parsed, ok := ParseProgressCardPayload(payload)
	if !ok {
		t.Fatalf("ParseProgressCardPayload should succeed, payload=%q", payload)
	}
	if parsed.Version != 2 {
		t.Fatalf("version = %d, want 2", parsed.Version)
	}
	if parsed.Agent != "Codex" {
		t.Fatalf("agent = %q, want Codex", parsed.Agent)
	}
	if parsed.Lang != string(LangChinese) {
		t.Fatalf("lang = %q, want %q", parsed.Lang, LangChinese)
	}
	if parsed.State != ProgressCardStateRunning {
		t.Fatalf("state = %q, want %q", parsed.State, ProgressCardStateRunning)
	}
	if len(parsed.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(parsed.Items))
	}
	if parsed.Items[1].Kind != ProgressEntryToolUse || parsed.Items[1].Tool != "Bash" {
		t.Fatalf("items[1] = %#v, want tool_use/Bash", parsed.Items[1])
	}
}

func TestParseProgressCardPayloadRejectsInvalid(t *testing.T) {
	if _, ok := ParseProgressCardPayload("plain text"); ok {
		t.Fatal("expected parse failure for plain text")
	}
	if _, ok := ParseProgressCardPayload(ProgressCardPayloadPrefix + "{not-json"); ok {
		t.Fatal("expected parse failure for invalid json")
	}
	if _, ok := ParseProgressCardPayload(ProgressCardPayloadPrefix + `{"entries":[]}`); ok {
		t.Fatal("expected parse failure for empty entries")
	}
}

func TestCompactProgressWriter_AppliesTransformToCardPayloadEntries(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
		supportPayload:     true,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "codex", LangEnglish, func(s string) string {
		return strings.ReplaceAll(s, "/root/code/demo/src/app.ts:42", "📄 `src/app.ts:42`")
	}, 0, "")

	if ok := w.AppendStructured(ProgressCardEntry{
		Kind: ProgressEntryThinking,
		Text: "Inspect /root/code/demo/src/app.ts:42",
	}, "Inspect /root/code/demo/src/app.ts:42"); !ok {
		t.Fatal("AppendStructured() = false, want true")
	}

	starts := p.getPreviewStarts()
	if len(starts) != 1 {
		t.Fatalf("preview starts = %d, want 1", len(starts))
	}
	payload, ok := ParseProgressCardPayload(starts[0])
	if !ok {
		t.Fatalf("ParseProgressCardPayload(%q) failed", starts[0])
	}
	if len(payload.Items) != 1 {
		t.Fatalf("payload items = %d, want 1", len(payload.Items))
	}
	if got := payload.Items[0].Text; got != "Inspect 📄 `src/app.ts:42`" {
		t.Fatalf("payload item text = %q, want transformed text", got)
	}
}

type stubThrottledProgressPlatform struct {
	stubCompactProgressPlatform
	throttle time.Duration
}

func (p *stubThrottledProgressPlatform) ProgressUpdateInterval() time.Duration {
	return p.throttle
}

func TestCompactProgressWriter_ThrottlesRapidUpdates(t *testing.T) {
	p := &stubThrottledProgressPlatform{
		stubCompactProgressPlatform: stubCompactProgressPlatform{
			stubPlatformEngine: stubPlatformEngine{n: "discord"},
			style:              "card",
			supportPayload:     true,
		},
		throttle: time.Second,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil, 0, "")
	clock := &manualProgressClock{now: time.Unix(1_700_000_000, 0)}
	w.now = func() time.Time { return clock.now }
	w.afterFunc = clock.after

	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryThinking, Text: "step 1"}, "step 1")
	if len(p.getPreviewStarts()) != 1 {
		t.Fatal("first update should create the preview message")
	}

	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryToolUse, Tool: "Bash", Text: "pwd"}, "pwd")
	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryToolResult, Tool: "Bash", Text: "ok"}, "ok")
	if edits := len(p.getPreviewEdits()); edits != 0 {
		t.Fatalf("rapid updates within throttle window should be skipped, got %d edits", edits)
	}
	if clock.pending == nil {
		t.Fatal("throttled edit should schedule a trailing update")
	}

	// The trailing timer sends the latest snapshot without another event.
	clock.fire()
	edits := p.getPreviewEdits()
	if len(edits) != 1 {
		t.Fatalf("trailing update edits = %d, want 1", len(edits))
	}
	payload, parsed := ParseProgressCardPayload(edits[0])
	if !parsed || len(payload.Items) != 3 {
		t.Fatalf("trailing snapshot = %#v", payload)
	}

	clock.now = clock.now.Add(time.Second)
	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryThinking, Text: "step 4"}, "step 4")
	if len(p.getPreviewEdits()) != 2 {
		t.Fatalf("update after throttle interval should go through, got %d edits", len(p.getPreviewEdits()))
	}

	ok := w.Finalize(ProgressCardStateCompleted)
	if !ok {
		t.Fatal("Finalize should succeed")
	}
	finalEdits := p.getPreviewEdits()
	last := finalEdits[len(finalEdits)-1]
	payload, parsed = ParseProgressCardPayload(last)
	if !parsed {
		t.Fatalf("final edit should be a valid payload, got %q", last)
	}
	if payload.State != ProgressCardStateCompleted {
		t.Fatalf("state = %q, want completed", payload.State)
	}
	if len(payload.Items) != 4 {
		t.Fatalf("items = %d, want 4 (all buffered items)", len(payload.Items))
	}
}

func TestCompactProgressWriter_DoesNotTransformToolResults(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
		supportPayload:     true,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "codex", LangEnglish, func(s string) string {
		return strings.ReplaceAll(s, "/root/code/demo/src/app.ts:42", "📄 `src/app.ts:42`")
	}, 0, "")

	raw := "/root/code/demo/src/app.ts:42"
	if ok := w.AppendStructured(ProgressCardEntry{
		Kind: ProgressEntryToolResult,
		Text: raw,
	}, raw); !ok {
		t.Fatal("AppendStructured() = false, want true")
	}

	starts := p.getPreviewStarts()
	if len(starts) != 1 {
		t.Fatalf("preview starts = %d, want 1", len(starts))
	}
	payload, ok := ParseProgressCardPayload(starts[0])
	if !ok {
		t.Fatalf("ParseProgressCardPayload(%q) failed", starts[0])
	}
	if got := payload.Items[0].Text; got != raw {
		t.Fatalf("tool result text = %q, want raw %q", got, raw)
	}
}

type manualProgressClock struct {
	mu      sync.Mutex
	now     time.Time
	pending func()
	delay   time.Duration
}

func (c *manualProgressClock) after(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = f
	c.delay = d
	return func() bool {
		c.mu.Lock()
		c.pending = nil
		c.mu.Unlock()
		return true
	}
}

func (c *manualProgressClock) fire() {
	c.mu.Lock()
	fn := c.pending
	c.pending = nil
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

type scriptedProgressPlatform struct {
	stubCompactProgressPlatform
	failUpdates int // >0 remaining failures; <0 always fail
	failCreates int
	sendErr     error
}

func (p *scriptedProgressPlatform) UpdateMessage(ctx context.Context, handle any, content string) error {
	if p.failUpdates < 0 || p.failUpdates > 0 {
		if p.failUpdates > 0 {
			p.failUpdates--
		}
		return errors.New("transient update")
	}
	return p.stubCompactProgressPlatform.UpdateMessage(ctx, handle, content)
}

func (p *scriptedProgressPlatform) SendPreviewStart(ctx context.Context, replyCtx any, content string) (any, error) {
	if p.failCreates < 0 || p.failCreates > 0 {
		if p.failCreates > 0 {
			p.failCreates--
		}
		return nil, errors.New("create failed")
	}
	return p.stubCompactProgressPlatform.SendPreviewStart(ctx, replyCtx, content)
}

func (p *scriptedProgressPlatform) Send(ctx context.Context, replyCtx any, content string) error {
	if err := p.stubCompactProgressPlatform.Send(ctx, replyCtx, content); err != nil {
		return err
	}
	return p.sendErr
}

func newScriptedProgress(failUpdates, failCreates int) (*scriptedProgressPlatform, *compactProgressWriter, *manualProgressClock) {
	p := &scriptedProgressPlatform{
		stubCompactProgressPlatform: stubCompactProgressPlatform{
			stubPlatformEngine: stubPlatformEngine{n: "card"},
			style:              "card",
			supportPayload:     true,
		},
		failUpdates: failUpdates,
		failCreates: failCreates,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "agent", LangEnglish, nil, 0, "Progress (live card unavailable)")
	clock := &manualProgressClock{now: time.Unix(1_700_000_000, 0)}
	w.now = func() time.Time { return clock.now }
	w.afterFunc = clock.after
	return p, w, clock
}

func TestProgressWriter_TransientUpdateFailureDoesNotFallBack(t *testing.T) {
	p, w, clock := newScriptedProgress(1, 0)
	if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryThinking, Text: "one"}, "one") {
		t.Fatal("first append should be handled")
	}
	if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryToolUse, Tool: "Bash", Text: "two"}, "two") {
		t.Fatal("update failure must not fall back to per-event send")
	}
	if sent := p.getSent(); len(sent) != 0 {
		t.Fatalf("transient update failure sent chat messages: %#v", sent)
	}
	if starts := p.getPreviewStarts(); len(starts) != 1 {
		t.Fatalf("starts = %d, want 1", len(starts))
	}
	if edits := p.getPreviewEdits(); len(edits) != 0 {
		t.Fatalf("failed update should not record an edit, got %d", len(edits))
	}
	clock.fire()
	edits := p.getPreviewEdits()
	if len(edits) != 1 {
		t.Fatalf("retry edits = %d, want 1", len(edits))
	}
	payload, ok := ParseProgressCardPayload(edits[0])
	if !ok || len(payload.Items) != 2 || payload.Items[0].Text != "one" || payload.Items[1].Text != "two" {
		t.Fatalf("retry payload = %#v", payload)
	}
	if sent := p.getSent(); len(sent) != 0 {
		t.Fatalf("retry should edit the card, not send, got %#v", sent)
	}
}

func TestProgressWriter_RolloverAfterThreeUpdateFailures(t *testing.T) {
	p, w, clock := newScriptedProgress(-1, 0)
	if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryThinking, Text: "seed"}, "seed") {
		t.Fatal("create failed")
	}
	// Two append+retry pairs reach the third UpdateMessage failure and roll over.
	for i := 0; i < 2; i++ {
		text := "u" + strconv.Itoa(i)
		if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryInfo, Text: text}, text) {
			t.Fatalf("append %d returned false", i)
		}
		clock.fire()
	}
	starts := p.getPreviewStarts()
	if len(starts) != 2 {
		t.Fatalf("starts after first rollover = %d, want 2 (original + rollover)", len(starts))
	}
	payload, ok := ParseProgressCardPayload(starts[1])
	if !ok {
		t.Fatalf("rollover payload did not parse: %q", starts[1])
	}
	joined := ""
	for _, item := range payload.Items {
		joined += item.Text + "|"
	}
	if !strings.Contains(joined, "seed") || !strings.Contains(joined, "u1") {
		t.Fatalf("rollover snapshot = %q, want the full current entries", joined)
	}

	for i := 0; i < 2; i++ {
		text := "v" + strconv.Itoa(i)
		if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryInfo, Text: text}, text) {
			t.Fatal("append returned false")
		}
		clock.fire()
	}
	if starts = p.getPreviewStarts(); len(starts) != 3 {
		t.Fatalf("starts after second rollover = %d, want 3", len(starts))
	}

	for i := 0; i < 2; i++ {
		text := "w" + strconv.Itoa(i)
		if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryInfo, Text: text}, text) {
			t.Fatal("append returned false")
		}
		clock.fire()
	}
	if starts = p.getPreviewStarts(); len(starts) != 3 {
		t.Fatalf("rollovers exceeded 2, starts = %d", len(starts))
	}
	sent := p.getSent()
	if len(sent) == 0 || !strings.Contains(sent[0], "Progress (live card unavailable)") {
		t.Fatalf("exhausted rollovers should digest, sent = %#v", sent)
	}
}

func TestProgressWriter_DigestCoalescesAndFlushesOnFinalize(t *testing.T) {
	p, w, clock := newScriptedProgress(0, -1)
	const n = 6
	for i := 0; i < n; i++ {
		text := "e" + strconv.Itoa(i)
		if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryThinking, Text: text}, text) {
			t.Fatalf("append %d returned false", i)
		}
		if i == 0 {
			clock.fire() // second create failure switches to digest; do not flush it
		}
	}
	if sent := p.getSent(); len(sent) != 0 {
		t.Fatalf("digest should wait for the flush threshold or Finalize, sent %#v", sent)
	}
	if !w.Finalize(ProgressCardStateCompleted) {
		t.Fatal("finalize should flush the digest")
	}
	if !w.Finalize(ProgressCardStateCompleted) {
		t.Fatal("second finalize must be safe")
	}
	sent := p.getSent()
	if len(sent) == 0 || len(sent) >= n {
		t.Fatalf("digest sends = %d, want a bounded count below %d: %#v", len(sent), n, sent)
	}
	for _, msg := range sent {
		if !strings.HasPrefix(msg, "Progress (live card unavailable)\n") {
			t.Fatalf("digest missing header: %q", msg)
		}
	}
	body := strings.Join(sent, "\n")
	for i := 0; i < n; i++ {
		if !strings.Contains(body, "e"+strconv.Itoa(i)) {
			t.Fatalf("digest dropped e%d: %q", i, body)
		}
	}
}

func TestProgressWriter_DigestFlushesWhenBufferIsLarge(t *testing.T) {
	p, w, clock := newScriptedProgress(0, -1)
	if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryInfo, Text: "small"}, "small") {
		t.Fatal("append returned false")
	}
	clock.fire() // second create failure enters digest
	big := strings.Repeat("字", 3000)
	if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryInfo, Text: big}, big) {
		t.Fatal("large append returned false")
	}
	sent := p.getSent()
	if len(sent) != 1 {
		t.Fatalf("large digest sends = %d, want 1", len(sent))
	}
	if !strings.Contains(sent[0], "Progress (live card unavailable)") || !strings.Contains(sent[0], "字") {
		t.Fatalf("large digest = %q", sent[0][:min(80, len(sent[0]))])
	}
}

func TestProgressWriter_DigestSendFailureDropsBatch(t *testing.T) {
	p, w, clock := newScriptedProgress(0, -1)
	p.sendErr = errors.New("send down")
	if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryInfo, Text: "batch"}, "batch") {
		t.Fatal("append returned false")
	}
	clock.fire()
	if !w.Finalize(ProgressCardStateFailed) {
		t.Fatal("finalize should still return true in digest mode")
	}
	if got := len(p.getSent()); got != 1 {
		t.Fatalf("failed digest sends = %d, want 1 dropped attempt", got)
	}
	if !w.Finalize(ProgressCardStateFailed) {
		t.Fatal("second finalize")
	}
	if got := len(p.getSent()); got != 1 {
		t.Fatalf("second finalize retried the digest, sends = %d", got)
	}
}

func TestProgressWriter_ContextCanceledSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &scriptedProgressPlatform{
		stubCompactProgressPlatform: stubCompactProgressPlatform{
			stubPlatformEngine: stubPlatformEngine{n: "card"},
			style:              "card",
			supportPayload:     true,
		},
	}
	w := newCompactProgressWriter(ctx, p, "ctx", "agent", LangEnglish, nil, 0, "Progress (live card unavailable)")
	if !w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryThinking, Text: "late"}, "late") {
		t.Fatal("canceled writer should not ask the engine to fall back")
	}
	if !w.Finalize(ProgressCardStateFailed) {
		t.Fatal("finalize during shutdown should stop quietly")
	}
	if starts, sent := p.getPreviewStarts(), p.getSent(); len(starts) != 0 || len(sent) != 0 {
		t.Fatalf("canceled writer sent starts=%d sent=%d", len(starts), len(sent))
	}
}

func TestProgressEditIntervalUsesTheLargerGap(t *testing.T) {
	platform := &stubThrottledProgressPlatform{throttle: 2 * time.Second}
	if got := progressEditInterval(time.Second, platform); got != 2*time.Second {
		t.Fatalf("platform longer gap = %s", got)
	}
	if got := progressEditInterval(5*time.Second, platform); got != 5*time.Second {
		t.Fatalf("core longer gap = %s", got)
	}
	if got := progressEditInterval(0, platform); got != 2*time.Second {
		t.Fatalf("core disabled = %s, want platform gap", got)
	}
	if got := progressEditInterval(0, &stubPlatformEngine{n: "plain"}); got != 0 {
		t.Fatalf("no throttler = %s, want 0", got)
	}
}
