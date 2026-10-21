package dsh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// errPermissionModeNotApplied is the user-visible startup warning when a
// resumed session's log cannot be updated. The detail stays in the log.
var errPermissionModeNotApplied = errors.New("Permission mode could not be applied to this resumed session. Use /new to start a conversation in the selected mode.")

type permissionFacts struct {
	preset      string
	sandbox     string
	approval    string
	sawPreset   bool
	sawSandbox  bool
	sawApproval bool
}

type storedEvent struct {
	Type string          `json:"type"`
	Seq  *int64          `json:"seq"`
	Data json.RawMessage `json:"data"`
}

// prepareResume updates a resumed session's permission facts before the
// ACP process is spawned. DSH_PERMISSION_MODE only seeds a fresh session;
// a resumed session replays the facts stored in its log.
func (a *Agent) prepareResume(ctx context.Context, sessionID string) error {
	if err := a.reconcileResumePermission(ctx, sessionID); err != nil {
		slog.Warn("dsh: could not apply permission mode to resumed session", "session_id", sessionID, "error", err)
		return errPermissionModeNotApplied
	}
	return nil
}

func (a *Agent) reconcileResumePermission(ctx context.Context, sessionID string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	sessionID = strings.TrimSpace(sessionID)
	if !safeSessionID(sessionID) {
		return fmt.Errorf("dsh: invalid session id")
	}
	dir, meta, err := a.findSessionDir(sessionID)
	if err != nil {
		if errors.Is(err, errSessionNotFound) {
			return nil
		}
		return err
	}
	if meta.ID != sessionID {
		return fmt.Errorf("dsh: session id does not match log header")
	}
	if err := rejectLocked(dir); err != nil {
		return err
	}
	logPath, ok := pickSessionLog(dir)
	if !ok {
		return fmt.Errorf("dsh: no session log")
	}
	plain, err := decodeSessionPlain(logPath)
	if err != nil {
		return err
	}
	lines, err := parseLogLines(plain)
	if err != nil {
		return err
	}
	next, err := nextEventSeq(lines)
	if err != nil {
		return err
	}
	folded, err := foldPermission(lines)
	if err != nil {
		return err
	}
	records, err := permissionDiff(folded, desiredPermission(a.GetMode()), next, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := rejectLocked(dir); err != nil {
		return err
	}
	frame, err := encodeZstdFrame(records)
	if err != nil {
		return err
	}
	return appendZstdFrame(logPath, frame)
}

func desiredPermission(mode string) permissionFacts {
	_, envVal, ok := canonicalMode(mode)
	if !ok {
		envVal = envWorkspaceWrite
	}
	switch envVal {
	case envReadOnly:
		return permissionFacts{preset: envReadOnly, sandbox: envReadOnly, approval: "ask"}
	case envFullAccess:
		return permissionFacts{preset: envFullAccess, sandbox: envFullAccess, approval: "never"}
	default:
		return permissionFacts{preset: envWorkspaceWrite, sandbox: envWorkspaceWrite, approval: "ask"}
	}
}

func foldPermission(lines []storedEvent) (permissionFacts, error) {
	var cur permissionFacts
	for _, line := range lines {
		switch line.Type {
		case "permission/preset":
			value, ok := dataString(line.Data, "preset")
			if !ok {
				return permissionFacts{}, fmt.Errorf("dsh: permission/preset data is not readable")
			}
			cur.preset = value
			cur.sawPreset = true
		case "sandbox/mode":
			value, ok := dataString(line.Data, "mode")
			if !ok {
				return permissionFacts{}, fmt.Errorf("dsh: sandbox/mode data is not readable")
			}
			cur.sandbox = value
			cur.sawSandbox = true
		case "approval/policy":
			value, ok := dataString(line.Data, "policy")
			if !ok {
				return permissionFacts{}, fmt.Errorf("dsh: approval/policy data is not readable")
			}
			cur.approval = value
			cur.sawApproval = true
		}
	}
	return cur, nil
}

func permissionDiff(cur, want permissionFacts, seq, now int64) ([]byte, error) {
	type change struct {
		kind  string
		key   string
		value string
	}
	var changes []change
	if !cur.sawPreset || cur.preset != want.preset {
		changes = append(changes, change{"permission/preset", "preset", want.preset})
	}
	if !cur.sawSandbox || cur.sandbox != want.sandbox {
		changes = append(changes, change{"sandbox/mode", "mode", want.sandbox})
	}
	if !cur.sawApproval || cur.approval != want.approval {
		changes = append(changes, change{"approval/policy", "policy", want.approval})
	}
	if len(changes) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	for i, change := range changes {
		raw, err := json.Marshal(struct {
			Type string            `json:"type"`
			Seq  int64             `json:"seq"`
			Time int64             `json:"time"`
			Data map[string]string `json:"data"`
		}{
			Type: change.kind,
			Seq:  seq + int64(i),
			Time: now,
			Data: map[string]string{change.key: change.value},
		})
		if err != nil {
			return nil, fmt.Errorf("dsh: encode permission event: %w", err)
		}
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func rejectLocked(dir string) error {
	locked, err := sessionDirLocked(dir)
	if err != nil {
		return fmt.Errorf("dsh: check session lock: %w", err)
	}
	if locked {
		return fmt.Errorf("dsh: session is in use")
	}
	return nil
}

func decodeSessionPlain(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dsh: read session log: %w", err)
	}
	if !strings.HasSuffix(path, ".zstd") {
		return raw, nil
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("dsh: zstd reader: %w", err)
	}
	defer dec.Close()
	plain, err := dec.DecodeAll(raw, nil)
	if err != nil {
		return nil, fmt.Errorf("dsh: decompress session log: %w", err)
	}
	return plain, nil
}

func parseLogLines(plain []byte) ([]storedEvent, error) {
	parts := bytes.Split(plain, []byte("\n"))
	var lines []storedEvent
	for i, part := range parts {
		part = bytes.TrimSpace(part)
		if len(part) == 0 {
			continue
		}
		var ev storedEvent
		if err := json.Unmarshal(part, &ev); err != nil {
			return nil, fmt.Errorf("dsh: session log line %d does not parse: %w", i+1, err)
		}
		if strings.TrimSpace(ev.Type) == "" {
			return nil, fmt.Errorf("dsh: session log line %d has no type", i+1)
		}
		lines = append(lines, ev)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("dsh: session log is empty")
	}
	return lines, nil
}

// nextEventSeq requires sequenced events to be 0,1,2,... with no gaps.
// A session header may omit seq. The returned value is the next seq to append.
func nextEventSeq(lines []storedEvent) (int64, error) {
	var expect int64
	for i, line := range lines {
		if line.Seq == nil {
			if line.Type == "session" {
				continue
			}
			return 0, fmt.Errorf("dsh: session log line %d has no seq", i+1)
		}
		if *line.Seq != expect {
			return 0, fmt.Errorf("dsh: session log seq discontinuity at %d", *line.Seq)
		}
		expect++
	}
	return expect, nil
}

func dataString(raw json.RawMessage, key string) (string, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", false
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return "", false
	}
	value, ok := payload[key].(string)
	if !ok {
		return "", false
	}
	return value, true
}

func encodeZstdFrame(plain []byte) ([]byte, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderCRC(true))
	if err != nil {
		return nil, fmt.Errorf("dsh: zstd writer: %w", err)
	}
	defer enc.Close()
	return enc.EncodeAll(plain, nil), nil
}

func appendZstdFrame(path string, frame []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("dsh: open session log: %w", err)
	}
	defer f.Close()
	n, err := f.Write(frame)
	if err != nil {
		return fmt.Errorf("dsh: append session log: %w", err)
	}
	if n != len(frame) {
		return fmt.Errorf("dsh: short append to session log")
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("dsh: sync session log: %w", err)
	}
	return nil
}
