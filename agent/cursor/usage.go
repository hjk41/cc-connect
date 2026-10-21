package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

var _ core.UsageReporter = (*Agent)(nil)

var errCursorUsageAuth = errors.New("cursor login expired")

var cursorUsageBaseURL = "https://api2.cursor.sh"
var cursorUsageClient = &http.Client{
	Timeout:       70 * time.Second,
	Transport:     &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// cursorUsageAuthPath mirrors the Cursor Agent CLI's own auth file location.
func cursorUsageAuthPath(goos, home string) string {
	switch goos {
	case "darwin":
		return filepath.Join(home, ".cursor", "auth.json")
	case "windows":
		root := os.Getenv("APPDATA")
		if root == "" {
			root = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(root, "Cursor", "auth.json")
	default:
		root := os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
		return filepath.Join(root, "cursor", "auth.json")
	}
}

func readCursorCLIAuthToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var auth struct {
		AccessToken      string `json:"accessToken"`
		AccessTokenSnake string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil {
		return "", err
	}
	token := strings.TrimSpace(auth.AccessToken)
	if token == "" {
		token = strings.TrimSpace(auth.AccessTokenSnake)
	}
	if token == "" {
		return "", fmt.Errorf("cursor: auth.json has no accessToken")
	}
	return token, nil
}

// cursorUsageTokens builds token candidates in priority order: an explicit
// CURSOR_AUTH_TOKEN, then the Cursor Agent CLI login. The CLI is the only
// supported login source; there is no desktop database fallback.
func cursorUsageTokens(goos, home string) []string {
	var tokens []string
	seen := make(map[string]bool)
	add := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" || seen[token] {
			return
		}
		seen[token] = true
		tokens = append(tokens, token)
	}
	add(os.Getenv("CURSOR_AUTH_TOKEN"))
	if token, err := readCursorCLIAuthToken(cursorUsageAuthPath(goos, home)); err == nil {
		add(token)
	}
	return tokens
}

func cursorUsageEndpoint() string {
	if endpoint := strings.TrimSpace(os.Getenv("CURSOR_API_ENDPOINT")); endpoint != "" {
		return endpoint
	}
	return cursorUsageBaseURL
}

// GetUsage reads the Cursor Agent CLI login and queries subscription quota without starting a session.
func (a *Agent) GetUsage(ctx context.Context) (*core.UsageReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	home, err := cursorUserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cursor: cannot locate login; run agent login")
	}
	tokens := cursorUsageTokens(runtime.GOOS, home)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("cursor: no saved login; run agent login")
	}
	endpoint := cursorUsageEndpoint()
	var lastErr error
	validToken := false
	for _, token := range tokens {
		for _, method := range []string{"GetCurrentPeriodUsage", "GetUsageLimitStatusAndActiveGrants"} {
			body, err := fetchCursorUsage(ctx, endpoint, token, method)
			if err != nil {
				lastErr = err
				if errors.Is(err, errCursorUsageAuth) {
					break
				}
				return nil, err
			}
			validToken = true
			windows, err := parseCursorQuotaWindows(body, time.Now().Unix())
			if err != nil {
				lastErr = err
				return nil, err
			}
			if len(windows) > 0 {
				return &core.UsageReport{Provider: "cursor", Buckets: []core.UsageBucket{{Name: "Cursor", Windows: windows}}}, nil
			}
		}
	}
	if !validToken && lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("cursor: account returned no subscription quota windows")
}

func fetchCursorUsage(ctx context.Context, endpoint, token, method string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/aiserver.v1.DashboardService/"+method, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("cursor: cannot create usage request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Content-Type", "application/json")
	// Also enforce redirect policy on injected clients, so credentials never follow redirects.
	client := *cursorUsageClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cursor: usage request failed: %s", core.RedactToken(err.Error(), token))
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("%w; run agent login or sign in to Cursor again", errCursorUsageAuth)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cursor: usage request failed (HTTP %d)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("cursor: cannot read usage response")
	}
	return body, nil
}

// usageNumber accepts numeric JSON strings as well as numbers, but never booleans.
func usageNumber(v any) (float64, bool) {
	var n float64
	switch v := v.(type) {
	case float64:
		n = v
	case string:
		var err error
		n, err = strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}

func cursorUsageWindow(label string, used, reset float64, now int64) core.UsageWindow {
	w := core.UsageWindow{Label: label, UsedPercent: int(math.Round(math.Max(0, math.Min(100, used)))), WindowSeconds: 30 * 86400}
	if reset > 0 && reset < float64(math.MaxInt64) {
		w.ResetAtUnix = int64(reset)
		if w.ResetAtUnix > now {
			w.ResetAfterSeconds = int(w.ResetAtUnix - now)
		}
	}
	return w
}

func parseCursorQuotaWindows(body []byte, now int64) ([]core.UsageWindow, error) {
	var data map[string]any
	if json.Unmarshal(body, &data) != nil {
		return nil, fmt.Errorf("cursor: invalid usage response")
	}
	plan, _ := data["planUsage"].(map[string]any)
	reset, _ := usageNumber(data["billingCycleEnd"])
	var windows []core.UsageWindow
	for _, field := range []struct{ key, label string }{{"apiPercentUsed", "Other models"}, {"autoPercentUsed", "Cursor Auto"}} {
		if used, ok := usageNumber(plan[field.key]); ok {
			windows = append(windows, cursorUsageWindow(field.label, used, reset/1000, now))
		}
	}
	if len(windows) == 0 {
		if used, ok := usageNumber(plan["totalPercentUsed"]); ok {
			windows = append(windows, cursorUsageWindow("Plan", used, reset/1000, now))
		} else if limit, ok := usageNumber(plan["limit"]); ok && limit > 0 {
			spend := 0.0
			valid := true
			if raw, exists := plan["includedSpend"]; exists {
				spend, valid = usageNumber(raw)
			}
			if valid {
				windows = append(windows, cursorUsageWindow("Plan", spend/limit*100, reset/1000, now))
			}
		}
	}
	if len(windows) > 0 {
		return windows, nil
	}
	grants, _ := data["activeGrants"].([]any)
	for _, raw := range grants {
		grant, _ := raw.(map[string]any)
		total, ok := usageNumber(grant["totalCents"])
		remaining, valid := usageNumber(grant["remainingCents"])
		if !ok || !valid || total <= 0 {
			continue
		}
		expiry, _ := usageNumber(grant["expiresAtMs"])
		windows = append(windows, cursorUsageWindow(cursorGrantLabel(grant), (1-remaining/total)*100, expiry/1000, now))
	}
	return windows, nil
}

func cursorGrantLabel(grant map[string]any) string {
	for _, key := range []string{"displayName", "name"} {
		if s, ok := grant[key].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	var models []string
	for _, key := range []string{"allowedModelTags", "allowedModelIds"} {
		values, _ := grant[key].([]any)
		for _, v := range values {
			if s, ok := v.(string); ok {
				models = append(models, strings.ToLower(s))
			}
		}
	}
	text := strings.Join(models, " ")
	for _, marker := range []string{"auto", "composer", "cursor", "grok"} {
		if strings.Contains(text, marker) {
			return "Cursor Auto"
		}
	}
	for _, marker := range []string{"third", "other", "claude", "gpt", "gemini", "o1", "o3", "o4"} {
		if strings.Contains(text, marker) {
			return "Other models"
		}
	}
	return "Plan"
}
