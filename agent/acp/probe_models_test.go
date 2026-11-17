package acp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// TestHelperProcessACPModelProbe re-execs the test binary as a fake ACP
// agent for AvailableModels probe tests. It answers initialize and
// session/new over newline-delimited JSON-RPC on stdio.
func TestHelperProcessACPModelProbe(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_MODEL_HELPER") != "1" {
		return
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := dec.Decode(&req); err != nil {
			os.Exit(0)
		}
		result := map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": 1,
				"agentCapabilities": map[string]any{
					"sessionCapabilities": map[string]any{
						"close": map[string]any{},
					},
				},
			}
		case "session/new":
			result = map[string]any{
				"sessionId": "probe-sess-1",
				"models": map[string]any{
					"currentModelId": "m-two",
					"availableModels": []any{
						map[string]any{"modelId": "m-one", "name": "Model One"},
						map[string]any{"modelId": "m-two", "name": "Model Two"},
					},
				},
			}
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}

// TestAvailableModels_ProbesBeforeFirstSession is the regression for the
// droid-bot /model bug: agents that only reveal their model catalog in
// session/new returned an empty list (and "(未设置)" current model) when
// /model ran before the first real session. AvailableModels must fall
// back to a one-shot probe and cache the result.
func TestAvailableModels_ProbesBeforeFirstSession(t *testing.T) {
	t.Setenv("GO_WANT_ACP_MODEL_HELPER", "1")
	a := &Agent{
		cmd:          "definitely-not-a-real-agent",
		ProbeCommand: os.Args[0],
		ProbeArgs:    []string{"-test.run=TestHelperProcessACPModelProbe"},
	}

	got := a.AvailableModels(context.Background())
	if len(got) != 2 {
		t.Fatalf("AvailableModels = %v, want 2 probed options", got)
	}
	if got[0].Name != "m-one" || got[1].Name != "m-two" {
		t.Fatalf("AvailableModels names = %v, want [m-one m-two]", got)
	}
	// The probed currentModelId must surface as the reported model so
	// the /model card shows what the agent will actually use.
	if cur := a.GetModel(); cur != "m-two" {
		t.Fatalf("GetModel = %q, want probed current %q", cur, "m-two")
	}
	// Second call serves the cache; no second probe.
	if again := a.AvailableModels(context.Background()); len(again) != 2 {
		t.Fatalf("cached AvailableModels = %v, want 2 options", again)
	}
}

// TestAvailableModels_ProbeNotRetriedOnFailure ensures a failed probe is
// not retried on every /model call (each retry would respawn the agent
// and stall the command for the full timeout).
func TestAvailableModels_ProbeNotRetriedOnFailure(t *testing.T) {
	a := &Agent{
		cmd:          "definitely-not-a-real-agent",
		ProbeCommand: "/nonexistent/acp-probe-bin",
	}
	if got := a.AvailableModels(context.Background()); len(got) != 0 {
		t.Fatalf("AvailableModels = %v, want empty on probe failure", got)
	}
	if !a.modelsProbed {
		t.Fatal("modelsProbed = false after failed probe, want true")
	}
}

// TestAvailableModels_ConfiguredSkipsProbe ensures the configured
// "models" option wins without spawning a probe.
func TestAvailableModels_ConfiguredSkipsProbe(t *testing.T) {
	a := &Agent{
		cmd:              "definitely-not-a-real-agent",
		ProbeCommand:     "/nonexistent/acp-probe-bin",
		configuredModels: modelOptionsFromStrings([]string{"cfg-model"}),
	}
	got := a.AvailableModels(context.Background())
	if len(got) != 1 || got[0].Name != "cfg-model" {
		t.Fatalf("AvailableModels = %v, want configured cfg-model", got)
	}
	if a.modelsProbed {
		t.Fatal("modelsProbed = true, probe must not run when configured models exist")
	}
}
