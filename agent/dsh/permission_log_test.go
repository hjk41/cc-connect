package dsh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcilePermissionAppendsDifferingFacts(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	id := "77777777-7777-7777-7777-777777777777"
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	agent.SetMode("plan")

	header := sessionHeaderLine(id, work)
	events := strings.Join([]string{
		`{"type":"permission/preset","seq":0,"time":1,"data":{"preset":"workspace-write"}}`,
		`{"type":"sandbox/mode","seq":1,"time":1,"data":{"mode":"workspace-write"}}`,
		`{"type":"approval/policy","seq":2,"time":1,"data":{"policy":"ask"}}`,
	}, "\n") + "\n"
	path := writeMultiFrameLog(t, home, id, []byte(header+"\n"), []byte(events))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := agent.prepareResume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(after, before) {
		t.Fatal("append rewrote existing log bytes")
	}
	if bytes.Equal(after, before) {
		t.Fatal("matching log was not extended")
	}

	plain, err := decodeSessionPlain(path)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := parseLogLines(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 6 {
		t.Fatalf("lines = %d, want header + 3 original + 2 appended", len(lines))
	}
	next, err := nextEventSeq(lines)
	if err != nil {
		t.Fatal(err)
	}
	if next != 5 {
		t.Fatalf("next seq = %d, want 5", next)
	}
	folded, err := foldPermission(lines)
	if err != nil {
		t.Fatal(err)
	}
	if folded.preset != "read-only" || folded.sandbox != "read-only" || folded.approval != "ask" {
		t.Fatalf("folded = %+v", folded)
	}
	if lines[4].Seq == nil || *lines[4].Seq != 3 || lines[4].Type != "permission/preset" {
		t.Fatalf("appended preset = %+v", lines[4])
	}
	if lines[5].Seq == nil || *lines[5].Seq != 4 || lines[5].Type != "sandbox/mode" {
		t.Fatalf("appended sandbox = %+v", lines[5])
	}
}

func TestReconcilePermissionNoopWhenMatching(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	id := "88888888-8888-8888-8888-888888888888"
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	agent.SetMode("plan")
	path := writePermLog(t, home, id, work,
		sessionHeaderLine(id, work),
		`{"type":"permission/preset","seq":0,"time":1,"data":{"preset":"read-only"}}`,
		`{"type":"sandbox/mode","seq":1,"time":1,"data":{"mode":"read-only"}}`,
		`{"type":"approval/policy","seq":2,"time":1,"data":{"policy":"ask"}}`,
	)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.prepareResume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("matching permission state was rewritten")
	}
}

func TestReconcilePermissionRefusesBadLastLine(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	id := "99999999-9999-9999-9999-999999999999"
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	agent.SetMode("plan")
	path := writePermLog(t, home, id, work,
		sessionHeaderLine(id, work),
		`{"type":"permission/preset","seq":0,"time":1,"data":{"preset":"workspace-write"}}`,
		`{not-json`,
	)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = agent.prepareResume(context.Background(), id)
	if !errors.Is(err, errPermissionModeNotApplied) {
		t.Fatalf("err = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("bad log was modified")
	}
}

func TestReconcilePermissionRefusesSeqGap(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	agent.SetMode("default")
	path := writePermLog(t, home, id, work,
		sessionHeaderLine(id, work),
		`{"type":"sandbox/mode","seq":0,"time":1,"data":{"mode":"read-only"}}`,
		`{"type":"sandbox/mode","seq":2,"time":1,"data":{"mode":"read-only"}}`,
	)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.prepareResume(context.Background(), id); !errors.Is(err, errPermissionModeNotApplied) {
		t.Fatalf("err = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("gapped log was modified")
	}
}

func TestReconcilePermissionRestoresDefault(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	id := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	agent.SetMode("default")
	writePermLog(t, home, id, work,
		sessionHeaderLine(id, work),
		`{"type":"permission/preset","seq":0,"time":1,"data":{"preset":"read-only"}}`,
		`{"type":"sandbox/mode","seq":1,"time":1,"data":{"mode":"read-only"}}`,
		`{"type":"approval/policy","seq":2,"time":1,"data":{"policy":"ask"}}`,
	)
	if err := agent.prepareResume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(writePermLogPath(home, id))
	plain, err := decodeSessionPlain(filepath.Join(dir, "session.v4.jsonl.zstd"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := parseLogLines(plain)
	if err != nil {
		t.Fatal(err)
	}
	folded, err := foldPermission(lines)
	if err != nil {
		t.Fatal(err)
	}
	if folded.preset != "workspace-write" || folded.sandbox != "workspace-write" || folded.approval != "ask" {
		t.Fatalf("folded = %+v", folded)
	}
}

func sessionHeaderLine(id, cwd string) string {
	raw, err := json.Marshal(map[string]any{
		"type": "session", "version": 4, "id": id, "createdAt": int64(1700000000000), "cwd": cwd,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func writePermLog(t *testing.T, home, id, cwd string, lines ...string) string {
	t.Helper()
	body := strings.Join(lines, "\n") + "\n"
	return writeMultiFrameLog(t, home, id, []byte(body))
}

func writeMultiFrameLog(t *testing.T, home, id string, frames ...[]byte) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "slug", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	for _, plain := range frames {
		frame, err := encodeZstdFrame(plain)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, frame...)
	}
	path := filepath.Join(dir, "session.v4.jsonl.zstd")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writePermLogPath(home, id string) string {
	return filepath.Join(home, "sessions", "slug", id, "session.v4.jsonl.zstd")
}
