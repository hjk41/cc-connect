package cursor

import (
	"context"
	"database/sql"
	"encoding/json"
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
	_ "modernc.org/sqlite"
)

var _ core.UsageReporter = (*Agent)(nil)

var cursorUsageBaseURL = "https://api2.cursor.sh"
var cursorUsageClient = &http.Client{
	Timeout:       70 * time.Second,
	Transport:     &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func cursorUsageDBPath(goos, home string) string {
	var root string
	switch goos {
	case "darwin":
		root = filepath.Join(home, "Library", "Application Support")
	case "windows":
		root = os.Getenv("APPDATA")
		if root == "" {
			root = filepath.Join(home, "AppData", "Roaming")
		}
	default:
		root = os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(root, "Cursor", "User", "globalStorage", "state.vscdb")
}

func readCursorUsageToken(ctx context.Context, path string) (string, error) {
	uri, err := sqliteURI(path, "ro")
	if err != nil {
		return "", fmt.Errorf("cursor: locate login database: %w", err)
	}
	db, err := sql.Open("sqlite", uri+"&_pragma=busy_timeout(2000)")
	if err != nil {
		return "", fmt.Errorf("cursor: cannot open login database; open Cursor and sign in")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var raw string
	if err := db.QueryRowContext(ctx, "SELECT value FROM ItemTable WHERE key='cursorAuth/accessToken' LIMIT 1;").Scan(&raw); err != nil {
		return "", fmt.Errorf("cursor: cannot read login database; open Cursor and sign in")
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, `"`) {
		var token string
		if json.Unmarshal([]byte(raw), &token) != nil {
			return "", fmt.Errorf("cursor: invalid saved login; sign in to Cursor again")
		}
		raw = strings.TrimSpace(token)
	}
	if raw == "" {
		return "", fmt.Errorf("cursor: no saved login; open Cursor and sign in")
	}
	return raw, nil
}

// GetUsage reads the desktop login and queries subscription quota without starting a session.
func (a *Agent) GetUsage(ctx context.Context) (*core.UsageReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	home, err := cursorUserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cursor: cannot locate login; open Cursor and sign in")
	}
	token, err := readCursorUsageToken(ctx, cursorUsageDBPath(runtime.GOOS, home))
	if err != nil {
		return nil, err
	}
	for _, method := range []string{"GetCurrentPeriodUsage", "GetUsageLimitStatusAndActiveGrants"} {
		body, err := fetchCursorUsage(ctx, token, method)
		if err != nil {
			return nil, err
		}
		windows, err := parseCursorQuotaWindows(body, time.Now().Unix())
		if err != nil {
			return nil, err
		}
		if len(windows) > 0 {
			return &core.UsageReport{Provider: "cursor", Buckets: []core.UsageBucket{{Name: "Cursor", Windows: windows}}}, nil
		}
	}
	return nil, fmt.Errorf("cursor: account returned no subscription quota windows")
}

func fetchCursorUsage(ctx context.Context, token, method string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cursorUsageBaseURL, "/")+"/aiserver.v1.DashboardService/"+method, strings.NewReader("{}"))
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
		return nil, fmt.Errorf("cursor: login expired; sign in to Cursor again")
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
