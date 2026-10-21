package acp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestMain(m *testing.M) {
	if os.Getenv("ACP_FAKE_SERVER") == "1" {
		serveFakeACP()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestStartSession_BeforeResumeRunsBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	marker := dir + "/spawned"
	var hookSawSpawn bool
	var calls int
	agent := &Agent{
		cmd:     os.Args[0],
		args:    []string{"-test.run=^$"},
		workDir: dir,
		extraEnv: []string{
			"ACP_FAKE_SERVER=1",
			"ACP_FAKE_MARKER=" + marker,
		},
	}
	agent.BeforeResume = func(context.Context, string) error {
		calls++
		if _, err := os.Stat(marker); err == nil {
			hookSawSpawn = true
		}
		return errors.New("permission mode could not be applied; use /new")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := agent.StartSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if calls != 1 {
		t.Fatalf("hook calls = %d, want 1", calls)
	}
	if hookSawSpawn {
		t.Fatal("resume hook ran after the process started")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("process did not start: %v", err)
	}
	warner, ok := sess.(core.StartupWarner)
	if !ok {
		t.Fatal("session has no startup warning")
	}
	if !strings.Contains(warner.StartupWarning(), "use /new") {
		t.Fatalf("warning = %q", warner.StartupWarning())
	}

	agent.BeforeResume = func(context.Context, string) error {
		t.Fatal("hook ran for a new session")
		return nil
	}
	fresh, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
}

func serveFakeACP() {
	if marker := os.Getenv("ACP_FAKE_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("1"), 0o600)
	}
	dec := json.NewDecoder(os.Stdin)
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		if len(bytesTrim(req.ID)) == 0 || string(req.ID) == "null" {
			continue
		}
		var result string
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":1,"agentCapabilities":{"loadSession":false,"sessionCapabilities":{"resume":{}}}}`
		case "session/resume":
			result = `{"sessionId":"kept"}`
		case "session/new":
			result = `{"sessionId":"fresh"}`
		default:
			_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"error":{"code":-32601,"message":"unexpected"}}` + "\n")
			continue
		}
		_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":` + result + "}\n")
	}
}

func bytesTrim(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}
