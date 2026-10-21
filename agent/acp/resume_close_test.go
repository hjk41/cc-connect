package acp

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandshake_ResumeWhenLoadUnsupported(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	s.workDir = t.TempDir()
	wantCwd := canonicalCwd(s.workDir)

	var mu sync.Mutex
	var methods []string
	var resumeCwd, resumeID string
	go scriptACP(t, rReq, wResp, func(method string, params json.RawMessage) (string, string) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		switch method {
		case "initialize":
			return `{"protocolVersion":1,"agentCapabilities":{"loadSession":false,"promptCapabilities":{"image":false},"sessionCapabilities":{"resume":{},"close":{},"list":{}}}}`, ""
		case "session/resume":
			var p struct {
				SessionID string `json:"sessionId"`
				Cwd       string `json:"cwd"`
			}
			_ = json.Unmarshal(params, &p)
			mu.Lock()
			resumeID = p.SessionID
			resumeCwd = p.Cwd
			mu.Unlock()
			return `{"sessionId":"kept","configOptions":[{"id":"model","category":"model","currentValue":"[\"prov\",\"m\"]","options":[{"group":"g","name":"Group","options":[{"value":"[\"prov\",\"m\"]","name":"Display"}]}]}]}`, ""
		default:
			return "", "unexpected " + method
		}
	})

	if err := s.handshake("kept", ""); err != nil {
		t.Fatal(err)
	}
	if s.currentACPSessionID() != "kept" {
		t.Fatalf("id = %q", s.currentACPSessionID())
	}
	if s.createdFresh {
		t.Fatal("resumed session must not be marked fresh")
	}
	if !s.resumeSupported || !s.closeSupported {
		t.Fatalf("resume=%v close=%v", s.resumeSupported, s.closeSupported)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(methods, ",") != "initialize,session/resume" {
		t.Fatalf("methods = %v", methods)
	}
	if resumeID != "kept" || resumeCwd != wantCwd {
		t.Fatalf("resume id=%q cwd=%q want cwd %q", resumeID, resumeCwd, wantCwd)
	}
	if s.GetModel() != `["prov","m"]` {
		t.Fatalf("model = %q", s.GetModel())
	}
}

func TestHandshake_LoadPreferredOverResume(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	var methods []string
	var mu sync.Mutex
	go scriptACP(t, rReq, wResp, func(method string, _ json.RawMessage) (string, string) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		switch method {
		case "initialize":
			return `{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"resume":{},"load":{}}}}`, ""
		case "session/load":
			return `{"sessionId":"loaded"}`, ""
		default:
			return "", "unexpected " + method
		}
	})
	if err := s.handshake("loaded", ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(methods, ",") != "initialize,session/load" {
		t.Fatalf("methods = %v", methods)
	}
}

func TestHandshake_ResumeFailureFallsThroughToNew(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	var methods []string
	var mu sync.Mutex
	go scriptACP(t, rReq, wResp, func(method string, _ json.RawMessage) (string, string) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		switch method {
		case "initialize":
			return `{"protocolVersion":1,"agentCapabilities":{"loadSession":false,"sessionCapabilities":{"resume":{}}}}`, ""
		case "session/resume":
			return "", "gone"
		case "session/new":
			return `{"sessionId":"fresh"}`, ""
		default:
			return "", "unexpected " + method
		}
	})
	if err := s.handshake("old", ""); err != nil {
		t.Fatal(err)
	}
	if s.currentACPSessionID() != "fresh" || !s.createdFresh {
		t.Fatalf("id=%q fresh=%v", s.currentACPSessionID(), s.createdFresh)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(methods, ",") != "initialize,session/resume,session/new" {
		t.Fatalf("methods = %v", methods)
	}
}

func TestClose_CallsSessionCloseWhenAdvertised(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	s.closeSupported = true
	s.setACPSessionID("sid-1")
	got := make(chan string, 1)
	go scriptACP(t, rReq, wResp, func(method string, params json.RawMessage) (string, string) {
		if method != "session/close" {
			return "", "unexpected " + method
		}
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &p)
		got <- p.SessionID
		return `{}`, ""
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-got:
		if id != "sid-1" {
			t.Fatalf("closed id = %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session/close was not called")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestClose_SkipsSessionCloseWhenNotAdvertised(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	s.closeSupported = false
	s.setACPSessionID("sid-1")
	called := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 1024)
		_, _ = rReq.Read(buf)
		called <- struct{}{}
	}()
	s.requestSessionClose()
	select {
	case <-called:
		t.Fatal("session/close was sent without the capability")
	case <-time.After(50 * time.Millisecond):
	}
	_ = wResp.Close()
}

func TestPreparePrompt_PrefixesOnlyFreshSession(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	s.createdFresh = true
	s.promptPrefix = "Format with mrkdwn."
	var n int
	go scriptACP(t, rReq, wResp, func(method string, params json.RawMessage) (string, string) {
		if method != "session/prompt" {
			return "", "unexpected " + method
		}
		n++
		has := strings.Contains(string(params), "Format with mrkdwn.")
		if n == 1 && !has {
			return "", "missing prefix: " + string(params)
		}
		if n > 1 && has {
			return "", "prefix repeated"
		}
		return `{}`, ""
	})
	if err := s.Send("hello", "m1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Send("again", "m2", nil, nil); err != nil {
		t.Fatal(err)
	}

	resumed, w2, r2 := newTestSession(t, nil)
	resumed.createdFresh = false
	resumed.promptPrefix = "Format with mrkdwn."
	go scriptACP(t, r2, w2, func(method string, params json.RawMessage) (string, string) {
		if strings.Contains(string(params), "Format with mrkdwn.") {
			return "", "resumed session was prefixed"
		}
		return `{}`, ""
	})
	if err := resumed.Send("hello", "m1", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMatchGroupedModelAndThoughtOptions(t *testing.T) {
	raw := []byte(`{
		"id":"model","category":"model","currentValue":"[\"deepseek-official\",\"deepseek-v4-flash\"]",
		"options":[{"group":"official","name":"Official","options":[
			{"value":"[\"deepseek-official\",\"deepseek-flash\"]","name":"DeepSeek-V4.1-Flash","description":"fast"},
			{"value":"[\"deepseek-official\",\"deepseek-v4-pro\"]","name":"DeepSeek-V4-Pro"},
			{"value":"[\"deepseek-official\",\"deepseek-v4-flash\"]","name":"deepseek-v4-flash"}
		]}]
	}`)
	var opt acpConfigOption
	if err := json.Unmarshal(raw, &opt); err != nil {
		t.Fatal(err)
	}
	models := modelOptionsFromConfig(opt)
	if len(models) != 3 || models[0].Desc != "DeepSeek-V4.1-Flash" || models[0].Name != `["deepseek-official","deepseek-flash"]` {
		t.Fatalf("models = %#v", models)
	}
	opaque := `["deepseek-official","deepseek-flash"]`
	if got := matchModelChoice(opt, opaque); got != opaque {
		t.Fatalf("opaque match = %q", got)
	}
	if got := matchModelChoice(opt, "deepseek-v4.1-flash"); got != opaque {
		t.Fatalf("display name match = %q", got)
	}
	if got := matchModelChoice(opt, "deepseek-v4-pro"); got != `["deepseek-official","deepseek-v4-pro"]` {
		t.Fatalf("inner id match = %q", got)
	}
	if got := innerModelID(opaque); got != "deepseek-flash" {
		t.Fatalf("inner = %q", got)
	}

	s, _, _ := newTestSession(t, nil)
	s.showModelDisplayName = true
	s.absorbConfigOptions([]acpConfigOption{opt})
	if s.GetModelDisplayName() != "deepseek-v4-flash" {
		t.Fatalf("display = %q", s.GetModelDisplayName())
	}

	thought := acpConfigOption{
		ID:       "reasoning_effort",
		Category: "thought_level",
		Options: []acpConfigOptionChoice{{
			Group: "effort",
			Options: []acpConfigOptionChoice{
				{Value: "off", Name: "Off"},
				{Value: "low", Name: "Low"},
				{Value: "high", Name: "High"},
				{Value: "max", Name: "Max"},
			},
		}},
	}
	matched, allowed := matchConfigChoice(thought, "High")
	if matched != "high" || len(allowed) != 4 {
		t.Fatalf("thought match=%q allowed=%v", matched, allowed)
	}
}
