package devin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDevinCredentialsPath(t *testing.T) {
	t.Setenv("AGENT_METER_DEVIN_CREDENTIALS", "/explicit/login.toml")
	t.Setenv("APPDATA", "/roaming")
	t.Setenv("XDG_DATA_HOME", "/xdg")
	for _, goos := range []string{"windows", "linux", "darwin"} {
		if got := devinCredentialsPath(goos, "/home/test"); got != "/explicit/login.toml" {
			t.Fatal(got)
		}
	}
	t.Setenv("AGENT_METER_DEVIN_CREDENTIALS", "~/login.toml")
	if got := devinCredentialsPath("linux", "/home/test"); got != "/home/test/login.toml" {
		t.Fatal(got)
	}
	t.Setenv("AGENT_METER_DEVIN_CREDENTIALS", "")
	if got := devinCredentialsPath("windows", "/home/test"); got != "/roaming/devin/credentials.toml" {
		t.Fatal(got)
	}
	if got := devinCredentialsPath("linux", "/home/test"); got != "/xdg/devin/credentials.toml" {
		t.Fatal(got)
	}
	t.Setenv("XDG_DATA_HOME", "")
	if got := devinCredentialsPath("darwin", "/home/test"); got != "/home/test/.local/share/devin/credentials.toml" {
		t.Fatal(got)
	}
	t.Setenv("APPDATA", "")
	if got := devinCredentialsPath("windows", "/home/test"); got != filepath.Join("/home/test", "AppData/Roaming/devin/credentials.toml") {
		t.Fatal(got)
	}
}

func writeUsageCredentials(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.toml")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDevinUsageCredentials(t *testing.T) {
	for _, server := range []string{"https://api.example.com", "https://api.example.com/", "https://api.example.com:443"} {
		path := writeUsageCredentials(t, fmt.Sprintf("\ufeffwindsurf_api_key = 'test-secret'\napi_server_url = '%s'\n", server))
		creds, err := readDevinUsageCredentials(path)
		if err != nil || creds.APIKey != "test-secret" || creds.Server != strings.TrimRight(server, "/") {
			t.Fatalf("valid credentials failed: %v", err)
		}
	}
	for _, server := range []string{"", "http://api.example.com", "https:///", "https://user:password@api.example.com", "https://api.example.com:444", "https://api.example.com/path", "https://api.example.com?query", "https://api.example.com?", "https://api.example.com#fragment", "https://api.example.com#", "https://api.example.com:bad", "//api.example.com"} {
		t.Run(server, func(t *testing.T) {
			path := writeUsageCredentials(t, fmt.Sprintf("windsurf_api_key = 'test-secret'\napi_server_url = '%s'", server))
			_, err := readDevinUsageCredentials(path)
			if err == nil || !strings.Contains(err.Error(), "api_server_url") || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("URL validation: %v", err)
			}
		})
	}
	for _, contents := range []string{"windsurf_api_key = 'test-secret", "api_server_url = 'https://api.example.com'", "windsurf_api_key = ''"} {
		_, err := readDevinUsageCredentials(writeUsageCredentials(t, contents))
		if err == nil || !strings.Contains(err.Error(), "login") || strings.Contains(err.Error(), "test-secret") {
			t.Fatalf("credential error: %v", err)
		}
	}
	if _, err := readDevinUsageCredentials(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("missing credentials: %v", err)
	}
}

func TestParseDevinUsage(t *testing.T) {
	const now int64 = 1700000000
	for _, tc := range []struct {
		name, body string
		used       []int
		seconds    []int
		reset      int64
		plan       string
	}{
		{"camel case", `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":75.4,"weeklyQuotaRemainingPercent":50,"dailyQuotaResetAtUnix":"1700000123","weeklyQuotaResetAtUnix":1700000123}},"planInfo":{"planName":"Pro"}}`, []int{25, 50}, []int{86400, 604800}, now + 123, "Pro"},
		{"snake case", `{"user_status":{"plan_status":{"daily_quota_remaining_percent":0,"weekly_quota_remaining_percent":100,"daily_quota_reset_at_unix":1700000123,"weekly_quota_reset_at_unix":"1700000123"}},"plan_info":{"plan_name":"Team"}}`, []int{100, 0}, []int{86400, 604800}, now + 123, "Team"},
		{"hidden daily", `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":1,"weeklyQuotaRemainingPercent":2}},"planInfo":{"hideDailyQuota":true}}`, []int{98}, []int{604800}, 0, ""},
		{"hidden weekly", `{"user_status":{"plan_status":{"daily_quota_remaining_percent":20,"weekly_quota_remaining_percent":30}},"plan_info":{"hide_weekly_quota":true}}`, []int{80}, []int{86400}, 0, ""},
		{"invalid daily", `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":-1,"weeklyQuotaRemainingPercent":20}}}`, []int{80}, []int{604800}, 0, ""},
		{"missing daily", `{"userStatus":{"planStatus":{"weeklyQuotaRemainingPercent":"20"}}}`, []int{80}, []int{604800}, 0, ""},
		{"past reset", `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":90,"dailyQuotaResetAtUnix":1600000000}}}`, []int{10}, []int{86400}, 1600000000, ""},
		{"invalid reset", `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":90,"dailyQuotaResetAtUnix":true}}}`, []int{10}, []int{86400}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := parseDevinUsage([]byte(tc.body), now)
			if err != nil {
				t.Fatal(err)
			}
			if report.Provider != "devin" || report.Plan != tc.plan || len(report.Buckets) != 1 {
				t.Fatalf("report: %+v", report)
			}
			var used, seconds []int
			for _, w := range report.Buckets[0].Windows {
				used = append(used, w.UsedPercent)
				seconds = append(seconds, w.WindowSeconds)
				after := 0
				if tc.reset > now {
					after = int(tc.reset - now)
				}
				if w.Label != "" || w.ResetAtUnix != tc.reset || w.ResetAfterSeconds != after {
					t.Fatalf("window: %+v", w)
				}
			}
			if !reflect.DeepEqual(used, tc.used) || !reflect.DeepEqual(seconds, tc.seconds) {
				t.Fatalf("used=%v seconds=%v", used, seconds)
			}
		})
	}
	for _, value := range []string{`null`, `true`, `false`, `-1`, `101`, `"invalid"`, `"NaN"`, `"Inf"`} {
		raw := fmt.Sprintf(`{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":%s}}}`, value)
		if _, err := parseDevinUsage([]byte(raw), now); err == nil {
			t.Errorf("accepted invalid remaining %s", value)
		}
	}
	for _, raw := range []string{`{}`, `{"userStatus":{"planStatus":{}}}`, `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":0,"weeklyQuotaRemainingPercent":0}},"planInfo":{"hideDailyQuota":true,"hideWeeklyQuota":true}}`, `not JSON`} {
		if _, err := parseDevinUsage([]byte(raw), now); err == nil {
			t.Errorf("accepted no quota: %s", raw)
		}
	}
}

type devinUsageRoundTripper func(*http.Request) (*http.Response, error)

func (f devinUsageRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func setupDevinUsage(t *testing.T) {
	t.Helper()
	oldClient, oldVersion, oldHome := devinUsageClient, devinUsageProtocolVersion, devinUsageHomeDir
	t.Cleanup(func() {
		devinUsageClient, devinUsageProtocolVersion, devinUsageHomeDir = oldClient, oldVersion, oldHome
	})
	devinUsageProtocolVersion = "test-version"
	home := t.TempDir()
	devinUsageHomeDir = func() (string, error) { return home, nil }
	t.Setenv("AGENT_METER_DEVIN_CREDENTIALS", writeUsageCredentials(t, "windsurf_api_key = 'devin-test-secret'\napi_server_url = 'https://devin.example'\n"))
}

func TestDevinGetUsageHTTP(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          int
		body, wantError string
	}{
		{"success", 200, `{"userStatus":{"planStatus":{"dailyQuotaRemainingPercent":30}}}`, ""},
		{"unauthorized", 401, `devin-test-secret`, "login expired"},
		{"forbidden", 403, `devin-test-secret`, "login expired"},
		{"server error", 500, `devin-test-secret`, "HTTP 500"},
		{"redirect", 302, `devin-test-secret`, "HTTP 302"},
		{"no windows", 200, `{}`, "no daily or weekly quota"},
		{"bad JSON", 200, `devin-test-secret`, "invalid usage response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupDevinUsage(t)
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/exa.seat_management_pb.SeatManagementService/GetUserStatus" {
					t.Errorf("request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Connect-Protocol-Version") != "1" {
					t.Error("invalid headers")
				}
				body, _ := io.ReadAll(r.Body)
				var payload struct {
					Metadata map[string]string `json:"metadata"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Error(err)
				}
				want := map[string]string{"apiKey": "devin-test-secret", "ideName": "devin-cli", "ideType": "chisel", "ideVersion": "test-version", "extensionName": "chisel", "extensionVersion": "test-version", "locale": "en", "os": runtime.GOOS}
				if !reflect.DeepEqual(payload.Metadata, want) {
					t.Error("invalid protocol metadata")
				}
				w.Header().Set("Location", "https://"+r.Host+"/redirected")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			transport := server.Client().Transport
			// Keep production origin validation; route only this test client to the local TLS server.
			devinUsageClient = &http.Client{Transport: devinUsageRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "devin.example" {
					t.Error("unexpected origin")
				}
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 45*time.Second {
					t.Error("missing usage deadline")
				}
				clone := r.Clone(r.Context())
				clone.URL.Host = target.Host
				return transport.RoundTrip(clone)
			})}
			report, err := (&Agent{}).GetUsage(context.Background())
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "devin-test-secret") {
					t.Fatalf("error: %v", err)
				}
			} else if err != nil || report.Provider != "devin" || report.Buckets[0].Windows[0].UsedPercent != 70 {
				t.Fatalf("usage failed: %v", err)
			}
			if calls != 1 {
				t.Fatalf("requests=%d, want 1 (no redirects)", calls)
			}
		})
	}
}

func TestDevinUsageTransportErrorRedactsKey(t *testing.T) {
	setupDevinUsage(t)
	devinUsageClient = &http.Client{Transport: devinUsageRoundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("failure devin-test-secret") })}
	_, err := (&Agent{}).GetUsage(context.Background())
	if err == nil || strings.Contains(err.Error(), "devin-test-secret") {
		t.Fatalf("error: %v", err)
	}
}

func TestParseDevinUsageIdentity(t *testing.T) {
	report, err := parseDevinUsage([]byte(`{"userStatus":{"email":"dev@example.com","planStatus":{"weeklyQuotaRemainingPercent":40}},"planInfo":{"planName":"Max"}}`), 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	if report.Email != "dev@example.com" || report.Plan != "Max" {
		t.Fatalf("identity = %q / %q", report.Email, report.Plan)
	}
}
