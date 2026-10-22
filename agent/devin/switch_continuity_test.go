package devin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestSwitchLaunchProcess(t *testing.T) {
	if os.Getenv("CC_DEVIN_SWITCH_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	var calls []json.RawMessage
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.ID) == 0 {
			continue
		}
		calls = append(calls, append(json.RawMessage(nil), scanner.Bytes()...))
		result := any(map[string]any{})
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}}
		case "session/load":
			result = map[string]any{"modes": map[string]any{"currentModeId": "accept-edits", "availableModes": []map[string]string{{"id": "bypass", "name": "Bypass"}, {"id": "accept-edits", "name": "Code"}}}}
		case "session/set_mode":
			data, _ := json.Marshal(calls)
			if err := os.WriteFile(os.Getenv("CC_DEVIN_SWITCH_CALLS"), data, 0600); err != nil {
				os.Exit(2)
			}
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		fmt.Println(string(response))
	}
	os.Exit(0)
}
func TestModeSwitch_ResumeLaunchAppliesSelectedMode(t *testing.T) {
	dir := t.TempDir()
	callsFile := filepath.Join(dir, "calls.json")
	raw, err := New(map[string]any{
		"cmd":      []string{os.Args[0], "-test.run=^TestSwitchLaunchProcess$", "--"},
		"work_dir": dir, "models_cmd": filepath.Join(dir, "no-models-cli"),
		"env": map[string]string{"CC_DEVIN_SWITCH_HELPER": "1", "CC_DEVIN_SWITCH_CALLS": callsFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := raw.(*Agent)
	a.SetMode("yolo")
	if applier, ok := any(a).(core.ResumeModeApplier); !ok || !applier.AppliesModeOnResume() {
		t.Fatal("Devin must preserve conversation on mode change")
	}
	if _, ok := any(a.Agent).(core.ResumeModeApplier); ok {
		t.Fatal("generic ACP must not claim all servers apply mode on resume")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := a.StartSession(ctx, "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.CurrentSessionID() != "resume-id" {
		t.Fatal("resume identity lost")
	}
	data, err := os.ReadFile(callsFile)
	if err != nil {
		t.Fatal(err)
	}
	var calls []struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
			ModeID    string `json:"modeId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &calls); err != nil {
		t.Fatal(err)
	}
	loaded, applied := false, false
	for _, call := range calls {
		if call.Method == "session/new" {
			t.Fatal("mode change created a fresh conversation")
		}
		if call.Method == "session/load" && call.Params.SessionID == "resume-id" {
			loaded = true
		}
		if call.Method == "session/set_mode" && call.Params.SessionID == "resume-id" && call.Params.ModeID == "bypass" {
			applied = loaded
		}
	}
	if !applied {
		t.Fatalf("mode not applied after load: %s", data)
	}
}
