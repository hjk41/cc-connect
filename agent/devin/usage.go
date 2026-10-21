package devin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/chenhg5/cc-connect/core"
)

var _ core.UsageReporter = (*Agent)(nil)

var devinUsageProtocolVersion = "3000.11.92"
var devinUsageHomeDir = os.UserHomeDir
var devinUsageClient = &http.Client{
	Timeout:       45 * time.Second,
	Transport:     &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext, TLSHandshakeTimeout: 15 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func devinCredentialsPath(goos, home string) string {
	if path := os.Getenv("AGENT_METER_DEVIN_CREDENTIALS"); path != "" {
		if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
			return filepath.Join(home, path[2:])
		}
		return path
	}
	var root string
	if goos == "windows" {
		root = os.Getenv("APPDATA")
		if root == "" {
			root = filepath.Join(home, "AppData", "Roaming")
		}
	} else {
		root = os.Getenv("XDG_DATA_HOME")
		if root == "" {
			root = filepath.Join(home, ".local", "share")
		}
	}
	return filepath.Join(root, "devin", "credentials.toml")
}

type devinUsageCredentials struct {
	APIKey string `toml:"windsurf_api_key"`
	Server string `toml:"api_server_url"`
}

func readDevinUsageCredentials(path string) (devinUsageCredentials, error) {
	var creds devinUsageCredentials
	raw, err := os.ReadFile(path)
	if err != nil {
		return creds, fmt.Errorf("devin: cannot read credentials; run devin auth login")
	}
	// Do not expose TOML decoder errors, which can include credential contents.
	if _, err := toml.Decode(strings.TrimPrefix(string(raw), "\ufeff"), &creds); err != nil {
		return devinUsageCredentials{}, fmt.Errorf("devin: invalid credentials TOML; run devin auth login")
	}
	creds.APIKey = strings.TrimSpace(creds.APIKey)
	if creds.APIKey == "" {
		return devinUsageCredentials{}, fmt.Errorf("devin: missing login key; run devin auth login")
	}
	u, err := url.Parse(creds.Server)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(creds.Server, "#") || (u.Path != "" && u.Path != "/") {
		return devinUsageCredentials{}, fmt.Errorf("devin: invalid api_server_url; expected an HTTPS origin on port 443")
	}
	creds.Server = strings.TrimRight(creds.Server, "/")
	return creds, nil
}

// GetUsage queries the CLI account's daily and weekly quota without starting a session.
func (a *Agent) GetUsage(ctx context.Context) (*core.UsageReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	home, err := devinUsageHomeDir()
	if err != nil {
		return nil, fmt.Errorf("devin: cannot locate credentials; run devin auth login")
	}
	creds, err := readDevinUsageCredentials(devinCredentialsPath(runtime.GOOS, home))
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"metadata": map[string]string{
		"apiKey": creds.APIKey, "ideName": "devin-cli", "ideType": "chisel", "ideVersion": devinUsageProtocolVersion,
		"extensionName": "chisel", "extensionVersion": devinUsageProtocolVersion, "locale": "en", "os": runtime.GOOS,
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, creds.Server+"/exa.seat_management_pb.SeatManagementService/GetUserStatus", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("devin: cannot create usage request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	client := *devinUsageClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("devin: usage request failed: %s", core.RedactToken(err.Error(), creds.APIKey))
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("devin: login expired; run devin auth login again")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("devin: usage request failed (HTTP %d)", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("devin: cannot read usage response")
	}
	return parseDevinUsage(raw, time.Now().Unix())
}

// The Connect service has returned both protobuf JSON and snake_case fields.
func devinUsageField(data map[string]any, snake string) any {
	if v, ok := data[snake]; ok {
		return v
	}
	parts := strings.Split(snake, "_")
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return data[strings.Join(parts, "")]
}

func devinUsageNumber(v any) (float64, bool) {
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

func parseDevinUsage(raw []byte, now int64) (*core.UsageReport, error) {
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil, fmt.Errorf("devin: invalid usage response")
	}
	user, _ := devinUsageField(data, "user_status").(map[string]any)
	plan, _ := devinUsageField(user, "plan_status").(map[string]any)
	info, _ := devinUsageField(data, "plan_info").(map[string]any)
	report := &core.UsageReport{Provider: "devin"}
	report.Plan, _ = devinUsageField(info, "plan_name").(string)
	report.Email, _ = devinUsageField(user, "email").(string)
	var windows []core.UsageWindow
	for _, period := range []struct {
		name    string
		seconds int
	}{{"daily", 86400}, {"weekly", 604800}} {
		if hidden, _ := devinUsageField(info, "hide_"+period.name+"_quota").(bool); hidden {
			continue
		}
		remaining, ok := devinUsageNumber(devinUsageField(plan, period.name+"_quota_remaining_percent"))
		if !ok || remaining < 0 || remaining > 100 {
			continue
		}
		w := core.UsageWindow{UsedPercent: int(math.Round(100 - remaining)), WindowSeconds: period.seconds}
		if reset, ok := devinUsageNumber(devinUsageField(plan, period.name+"_quota_reset_at_unix")); ok && reset > 0 && reset < float64(math.MaxInt64) {
			w.ResetAtUnix = int64(reset)
			if w.ResetAtUnix > now {
				w.ResetAfterSeconds = int(w.ResetAtUnix - now)
			}
		}
		windows = append(windows, w)
	}
	if len(windows) == 0 {
		return nil, fmt.Errorf("devin: account returned no daily or weekly quota windows")
	}
	report.Buckets = []core.UsageBucket{{Name: "Devin", Windows: windows}}
	return report, nil
}
