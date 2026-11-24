package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCronShellLongOutputHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CRON_SHELL_LONG_HELPER") != "1" {
		return
	}
	fmt.Println("DIGEST_HEAD_SENTINEL")
	// Emit >64KB on a single line plus multi-line digest entries to exercise both
	// the expanded scanner buffer and multi-chunk SplitMessageCodeFenceAware delivery.
	fmt.Println(strings.Repeat("x", 66*1024))
	for i := 0; i < 120; i++ {
		fmt.Printf("第%03d条：技术热帖摘要内容用于验证定时任务长输出不会被三千字硬截断\n", i)
	}
	fmt.Println("DIGEST_TAIL_SENTINEL：想拿哪条做评论帖？")
	os.Exit(0)
}

func TestExecuteCronShell_LongOutputSplitsWithoutTruncation(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	store, err := NewCronStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}
	scheduler := NewCronScheduler(store)
	platform := &stubCronReplyTargetPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
	}
	e := NewEngine("test", &stubAgent{}, []Platform{platform}, "", LangEnglish)
	defer e.cancel()
	e.cronScheduler = scheduler

	silent := true
	job := &CronJob{
		ID:          "digest-long",
		SessionKey:  "feishu:chat-1:user-1",
		Exec:        fmt.Sprintf("GO_WANT_CRON_SHELL_LONG_HELPER=1 %q -test.run=^TestCronShellLongOutputHelperProcess$", exe),
		Description: "morning digest",
		Silent:      &silent,
	}
	// On Windows cmd/powershell cannot parse POSIX inline env vars; write a wrapper script.
	scriptDir := t.TempDir()
	if os.PathSeparator == '\\' {
		bat := filepath.Join(scriptDir, "run.bat")
		content := fmt.Sprintf("@echo off\r\nset GO_WANT_CRON_SHELL_LONG_HELPER=1\r\n\"%s\" -test.run=^TestCronShellLongOutputHelperProcess$\r\n", exe)
		if err := os.WriteFile(bat, []byte(content), 0o755); err != nil {
			t.Fatalf("write bat: %v", err)
		}
		job.Exec = fmt.Sprintf("%q", bat)
	}

	if err := store.Add(job); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := e.ExecuteCronJob(job); err != nil {
		t.Fatalf("ExecuteCronJob: %v", err)
	}

	sent := platform.getSent()
	if len(sent) < 3 {
		t.Fatalf("expected long shell output to split into >=3 chunks, got %d", len(sent))
	}
	for i, chunk := range sent {
		if n := utf8.RuneCountInString(chunk); n > maxPlatformMessageLen {
			t.Errorf("chunk[%d] rune len = %d, exceeds maxPlatformMessageLen (%d)", i, n, maxPlatformMessageLen)
		}
	}
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "DIGEST_HEAD_SENTINEL") {
		t.Errorf("missing head sentinel in delivered chunks")
	}
	if !strings.Contains(joined, "DIGEST_TAIL_SENTINEL：想拿哪条做评论帖？") {
		t.Errorf("missing tail sentinel in delivered chunks (long output was truncated)")
	}
}

func TestSendToSession_LongMessageSplitsIntoChunks(t *testing.T) {
	platform := &stubCronReplyTargetPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
	}
	e := NewEngine("test", &stubAgent{}, []Platform{platform}, filepath.Join(t.TempDir(), "sessions.json"), LangEnglish)
	defer e.cancel()

	var sb strings.Builder
	sb.WriteString("## Session x — START\n")
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&sb, "- item %03d: 每日运营发布回报条目完整内容验证分段投递不丢字\n", i)
	}
	sb.WriteString("REPORT_END_SENTINEL")
	longMsg := sb.String()

	if err := e.SendToSession("feishu:chat-ops:user-1", longMsg); err != nil {
		t.Fatalf("SendToSession: %v", err)
	}

	sent := platform.getSent()
	if len(sent) < 2 {
		t.Fatalf("expected message (%d runes) to split into >=2 chunks, got %d", utf8.RuneCountInString(longMsg), len(sent))
	}
	for i, chunk := range sent {
		if n := utf8.RuneCountInString(chunk); n > maxPlatformMessageLen {
			t.Errorf("chunk[%d] rune len = %d, exceeds %d", i, n, maxPlatformMessageLen)
		}
	}
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "## Session x — START") || !strings.Contains(joined, "REPORT_END_SENTINEL") {
		t.Fatalf("delivered chunks lost start or end content")
	}
}
