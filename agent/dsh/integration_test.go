package dsh

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// TestIntegration_PromptResumeHistory drives a real `dsh --profile acp`
// process. It runs only when CC_CONNECT_DSH_INTEGRATION=1 and
// DEEPSEEK_API_KEY are set.
func TestIntegration_PromptResumeHistory(t *testing.T) {
	if os.Getenv("CC_CONNECT_DSH_INTEGRATION") != "1" {
		t.Skip("set CC_CONNECT_DSH_INTEGRATION=1 to run the live dsh test")
	}
	if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" {
		t.Skip("DEEPSEEK_API_KEY is not set")
	}
	if _, err := exec.LookPath("dsh"); err != nil {
		t.Skip("dsh is not on PATH")
	}

	work := t.TempDir()
	home := filepath.Join(t.TempDir(), "dsh-home")
	agent := testAgent(t, map[string]any{
		"cmd":      "dsh",
		"work_dir": work,
		"dsh_home": home,
		"model":    "deepseek-flash",
		"mode":     "yolo",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	sess, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	sid := sess.CurrentSessionID()
	if sid == "" {
		t.Fatal("empty session id")
	}
	t.Cleanup(func() {
		if sess.Alive() {
			_ = sess.Close()
		}
		_ = agent.DeleteSession(context.Background(), sid)
	})

	if err := sess.Send("reply with the word pong", "m1", nil, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	text := readUntilResult(t, sess, 3*time.Minute)
	if !strings.Contains(strings.ToLower(text), "pong") {
		t.Fatalf("reply = %q", text)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	agent.SetModel("deepseek-flash")
	resumed, err := agent.StartSession(ctx, sid)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	defer func() { _ = resumed.Close() }()
	if resumed.CurrentSessionID() != sid {
		t.Fatalf("resumed id = %q, want %q", resumed.CurrentSessionID(), sid)
	}

	listed, err := agent.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range listed {
		if item.ID == sid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("list = %#v, missing %s", listed, sid)
	}
	hist, err := agent.GetSessionHistory(ctx, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, entry := range hist {
		joined += "\n" + entry.Content
	}
	if !strings.Contains(joined, "reply with the word pong") {
		t.Fatalf("history = %#v", hist)
	}
}

// TestIntegration_ResumeAppliesPlanMode checks that /mode plan is enforced
// on the same dsh session after the process is restarted.
func TestIntegration_ResumeAppliesPlanMode(t *testing.T) {
	if os.Getenv("CC_CONNECT_DSH_INTEGRATION") != "1" {
		t.Skip("set CC_CONNECT_DSH_INTEGRATION=1 to run the live dsh test")
	}
	if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" {
		t.Skip("DEEPSEEK_API_KEY is not set")
	}
	if _, err := exec.LookPath("dsh"); err != nil {
		t.Skip("dsh is not on PATH")
	}

	work := t.TempDir()
	home := filepath.Join(t.TempDir(), "dsh-home")
	agent := testAgent(t, map[string]any{
		"cmd":      "dsh",
		"work_dir": work,
		"dsh_home": home,
		"model":    "deepseek-flash",
		"mode":     "default",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	sess, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	sid := sess.CurrentSessionID()
	if sid == "" {
		t.Fatal("empty session id")
	}
	t.Cleanup(func() {
		if sess.Alive() {
			_ = sess.Close()
		}
		_ = agent.DeleteSession(context.Background(), sid)
	})

	okFile := filepath.Join(work, "dsh-write-ok.txt")
	_, results := readTurn(t, sess, "Create the file dsh-write-ok.txt in the current working directory containing exactly the text ok. Use a tool. After it succeeds, reply with the single word wrote.", 3*time.Minute, true)
	if _, err := os.Stat(okFile); err != nil {
		t.Fatalf("write did not create %s: %v\nresults:\n%s", okFile, err, redactKey(strings.Join(results, "\n")))
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	agent.SetMode("plan")
	resumed, err := agent.StartSession(ctx, sid)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	defer func() { _ = resumed.Close() }()
	if warn, ok := resumed.(core.StartupWarner); ok && strings.TrimSpace(warn.StartupWarning()) != "" {
		t.Fatalf("startup warning: %s", redactKey(warn.StartupWarning()))
	}
	blocked := filepath.Join(work, "dsh-touch-blocked.txt")
	// Deny escalation. An approved retry can override the session sandbox
	// for that call; the read-only denial is the unapproved tool result.
	_, results = readTurn(t, resumed, "Run exactly this shell command and no other command: touch dsh-touch-blocked.txt", 3*time.Minute, false)
	joined := strings.ToLower(strings.Join(results, "\n"))
	if !strings.Contains(joined, "read-only") && !strings.Contains(joined, "operation not permitted") {
		t.Fatalf("tool results = %q", redactKey(strings.Join(results, "\n")))
	}
	if _, err := os.Stat(blocked); !os.IsNotExist(err) {
		t.Fatalf("blocked file exists or stat failed: %v", err)
	}
}

func readTurn(t *testing.T, sess core.AgentSession, prompt string, timeout time.Duration, allowPermission bool) (string, []string) {
	t.Helper()
	// session/prompt blocks until the turn finishes, and a permission
	// request is answered on this same test. Read events while Send runs.
	sendCh := make(chan error, 1)
	go func() {
		sendCh <- sess.Send(prompt, "m-turn", nil, nil)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var text strings.Builder
	var results []string
	var sendErr error
	sendDone := false
	gotResult := false
	for !sendDone || !gotResult {
		select {
		case err := <-sendCh:
			sendDone = true
			sendErr = err
			sendCh = nil
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatal("event channel closed")
			}
			switch ev.Type {
			case core.EventText:
				text.WriteString(ev.Content)
			case core.EventToolResult:
				body := ev.Content
				if ev.ToolResult != "" {
					body = ev.ToolResult
				}
				if strings.TrimSpace(body) != "" {
					results = append(results, body)
				}
			case core.EventPermissionRequest:
				behavior := "deny"
				if allowPermission {
					behavior = "allow"
				}
				if err := sess.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: behavior}); err != nil {
					t.Fatalf("respond permission: %v", err)
				}
			case core.EventError:
				if ev.Error != nil {
					t.Fatalf("agent error: %v", ev.Error)
				}
			case core.EventResult:
				if ev.Done {
					gotResult = true
				}
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for a reply, got %q results %q", redactKey(text.String()), redactKey(strings.Join(results, "\n")))
		}
	}
	if sendErr != nil {
		t.Fatalf("send: %v text=%q results=%q", sendErr, redactKey(text.String()), redactKey(strings.Join(results, "\n")))
	}
	return text.String(), results
}

func redactKey(s string) string {
	key := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "[redacted]")
}

func readUntilResult(t *testing.T, sess core.AgentSession, timeout time.Duration) string {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var text strings.Builder
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatal("event channel closed")
			}
			switch ev.Type {
			case core.EventText:
				text.WriteString(ev.Content)
			case core.EventError:
				t.Fatalf("agent error: %v", ev.Error)
			case core.EventResult:
				if ev.Done {
					return text.String()
				}
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for a reply, got %q", text.String())
		}
	}
}
