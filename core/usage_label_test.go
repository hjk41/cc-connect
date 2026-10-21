package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFormatUsageBlock_LabelOverridesDuration(t *testing.T) {
	window := &UsageWindow{Label: "Other models", WindowSeconds: 30 * 86400, UsedPercent: 25}
	for _, lang := range []Language{LangEnglish, LangChinese, LangTraditionalChinese, LangJapanese, LangSpanish} {
		got := formatUsageBlock(lang, window)
		if !strings.HasPrefix(got, "Other models\n") || !strings.Contains(got, "75%") {
			t.Fatalf("labelled window: %q", got)
		}
		window.Label = ""
		if got := formatUsageBlock(lang, window); !strings.HasPrefix(got, usageWindowLabel(lang, window.WindowSeconds)+"\n") {
			t.Fatalf("default label changed: %q", got)
		}
		window.Label = "Other models"
	}
}

type usageDeadlineAgent struct {
	stubAgent
	remaining time.Duration
}

func (a *usageDeadlineAgent) GetUsage(ctx context.Context) (*UsageReport, error) {
	deadline, _ := ctx.Deadline()
	a.remaining = time.Until(deadline)
	return &UsageReport{}, nil
}

func TestCmdUsage_AllowsSlowQuotaService(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &usageDeadlineAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.handleCommand(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}, "/usage")
	if agent.remaining < 74*time.Second || agent.remaining > 75*time.Second {
		t.Fatalf("usage timeout = %s, want 75s", agent.remaining)
	}
}
