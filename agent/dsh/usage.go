package dsh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

const defaultBalanceURL = "https://api.deepseek.com/user/balance"

// GetUsage queries the DeepSeek account balance. The API key is taken
// from the active provider, then the agent env, then the process
// environment. The key is never logged.
func (a *Agent) GetUsage(ctx context.Context) (*core.UsageReport, error) {
	key := a.apiKey()
	if key == "" {
		return nil, fmt.Errorf("dsh: DEEPSEEK_API_KEY is not set")
	}
	endpoint := defaultBalanceURL
	if a != nil {
		a.mu.Lock()
		if strings.TrimSpace(a.balanceURL) != "" {
			endpoint = a.balanceURL
		}
		a.mu.Unlock()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("dsh: balance request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dsh: balance request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("dsh: read balance response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("dsh: balance request failed: status %d", resp.StatusCode)
	}
	report, err := parseBalance(body)
	if err != nil {
		return nil, err
	}
	return report, nil
}

func (a *Agent) apiKey() string {
	if a == nil {
		return strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.activeProviderLocked(); p != nil {
		if key := strings.TrimSpace(p.APIKey); key != "" {
			return key
		}
		if p.Env != nil {
			if key := strings.TrimSpace(p.Env["DEEPSEEK_API_KEY"]); key != "" {
				return key
			}
		}
	}
	if a.configEnv != nil {
		if key := strings.TrimSpace(a.configEnv["DEEPSEEK_API_KEY"]); key != "" {
			return key
		}
	}
	return strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
}

type balancePayload struct {
	IsAvailable  bool `json:"is_available"`
	BalanceInfos []struct {
		Currency        string `json:"currency"`
		TotalBalance    string `json:"total_balance"`
		GrantedBalance  string `json:"granted_balance"`
		ToppedUpBalance string `json:"topped_up_balance"`
	} `json:"balance_infos"`
}

func parseBalance(body []byte) (*core.UsageReport, error) {
	var payload balancePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("dsh: parse balance response: %w", err)
	}
	var parts []string
	for _, info := range payload.BalanceInfos {
		total := strings.TrimSpace(info.TotalBalance)
		if total == "" {
			continue
		}
		cur := strings.TrimSpace(info.Currency)
		if cur == "" {
			parts = append(parts, total)
			continue
		}
		parts = append(parts, cur+" "+total)
	}
	balance := strings.Join(parts, ", ")
	plan := "DeepSeek"
	if !payload.IsAvailable {
		plan = "DeepSeek unavailable"
	}
	report := &core.UsageReport{
		Provider: "deepseek",
		Plan:     plan,
		Credits: &core.UsageCredits{
			HasCredits: payload.IsAvailable && balance != "",
			Unlimited:  false,
			Balance:    balance,
		},
	}
	return report, nil
}
