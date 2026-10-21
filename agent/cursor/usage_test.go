package cursor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func makeUsageDB(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	uri, err := sqliteURI(path, "rw")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", strings.Replace(uri, "mode=rw", "mode=rwc", 1))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO ItemTable VALUES (?, ?)", "cursorAuth/accessToken", value); err != nil {
		t.Fatal(err)
	}
}

func TestCursorUsageToken(t *testing.T) {
	for _, raw := range []string{"test-secret", `"test-secret"`} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "space #?", "state.vscdb")
			makeUsageDB(t, path, raw)
			token, err := readCursorUsageToken(context.Background(), path)
			if err != nil || token != "test-secret" {
				t.Fatalf("token read failed: %v", err)
			}
		})
	}
	for _, raw := range []string{"", `""`, `"invalid`} {
		path := filepath.Join(t.TempDir(), "state.vscdb")
		makeUsageDB(t, path, raw)
		if _, err := readCursorUsageToken(context.Background(), path); err == nil || !strings.Contains(err.Error(), "sign in") {
			t.Fatalf("want login error, got %v", err)
		}
	}
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := readCursorUsageToken(context.Background(), path); err == nil || !strings.Contains(err.Error(), "sign in") {
		t.Fatalf("missing database: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("read-only access created database: %v", err)
	}
	path = filepath.Join(t.TempDir(), "no-login.db")
	makeUsageDB(t, path, "temporary")
	db, _ := sql.Open("sqlite", path)
	if _, err := db.Exec("DELETE FROM ItemTable"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := readCursorUsageToken(context.Background(), path); err == nil || !strings.Contains(err.Error(), "sign in") {
		t.Fatalf("missing token: %v", err)
	}
}

func TestCursorUsageDBPaths(t *testing.T) {
	t.Setenv("APPDATA", "/roaming")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	for _, tc := range []struct{ os, root string }{{"darwin", "/home/test/Library/Application Support"}, {"windows", "/roaming"}, {"linux", "/xdg"}} {
		if got := cursorUsageDBPath(tc.os, "/home/test"); got != filepath.Join(tc.root, "Cursor/User/globalStorage/state.vscdb") {
			t.Errorf("%s: %s", tc.os, got)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := cursorUsageDBPath("linux", "/home/test"); got != "/home/test/.config/Cursor/User/globalStorage/state.vscdb" {
		t.Fatal(got)
	}
}

func TestParseCursorUsage(t *testing.T) {
	const now int64 = 1700000000
	for _, tc := range []struct {
		name, body string
		labels     []string
		used       []int
		reset      int64
	}{
		{"two pools", `{"billingCycleEnd":1700000123456,"planUsage":{"apiPercentUsed":24.6,"autoPercentUsed":0,"totalPercentUsed":99,"bonusSpend":900}}`, []string{"Other models", "Cursor Auto"}, []int{25, 0}, now + 123},
		{"total", `{"planUsage":{"totalPercentUsed":"32.5"}}`, []string{"Plan"}, []int{33}, 0},
		{"included cents", `{"planUsage":{"limit":2000,"includedSpend":500,"bonusSpend":10000},"spendLimitUsage":{"used":10000}}`, []string{"Plan"}, []int{25}, 0},
		{"included default zero", `{"planUsage":{"limit":2000}}`, []string{"Plan"}, []int{0}, 0},
		{"clamp", `{"planUsage":{"apiPercentUsed":-2,"autoPercentUsed":140}}`, []string{"Other models", "Cursor Auto"}, []int{0, 100}, 0},
		{"grants", `{"activeGrants":[{"totalCents":1000,"remainingCents":400,"displayName":"Custom","expiresAtMs":1700000123456},{"totalCents":100,"remainingCents":150,"name":"Named","expiresAtMs":1700000123456}]}`, []string{"Custom", "Named"}, []int{60, 0}, now + 123},
		{"derive tags", `{"activeGrants":[{"totalCents":100,"remainingCents":-10,"allowedModelTags":["composer"]},{"totalCents":100,"remainingCents":90,"allowedModelTags":["third-party"]},{"totalCents":100,"remainingCents":50}]}`, []string{"Cursor Auto", "Other models", "Plan"}, []int{100, 10, 50}, 0},
		{"derive model IDs", `{"activeGrants":[{"totalCents":100,"remainingCents":50,"allowedModelIds":["gpt-5"]}]}`, []string{"Other models"}, []int{50}, 0},
		{"past reset", `{"billingCycleEnd":1600000000000,"planUsage":{"apiPercentUsed":1}}`, []string{"Other models"}, []int{1}, 1600000000},
		{"ignore spend", `{"bonusSpend":100,"spendLimitUsage":{"limit":100},"onDemand":{"used":50},"activeGrants":[null,{"totalCents":0,"remainingCents":0},{"totalCents":100}]}`, nil, nil, 0},
		{"invalid numbers", `{"planUsage":{"apiPercentUsed":true,"autoPercentUsed":"NaN","totalPercentUsed":null,"limit":-1}}`, nil, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			windows, err := parseCursorQuotaWindows([]byte(tc.body), now)
			if err != nil {
				t.Fatal(err)
			}
			var labels []string
			var used []int
			for _, w := range windows {
				labels = append(labels, w.Label)
				used = append(used, w.UsedPercent)
				wantAfter := 0
				if tc.reset > now {
					wantAfter = int(tc.reset - now)
				}
				if w.WindowSeconds != 30*86400 || w.ResetAtUnix != tc.reset || w.ResetAfterSeconds != wantAfter {
					t.Fatalf("window: %+v", w)
				}
			}
			if !reflect.DeepEqual(labels, tc.labels) || !reflect.DeepEqual(used, tc.used) {
				t.Fatalf("labels=%v used=%v", labels, used)
			}
		})
	}
	if _, err := parseCursorQuotaWindows([]byte(`not JSON`), now); err == nil {
		t.Fatal("accepted invalid JSON")
	}
}

func setupCursorUsage(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	oldHome, oldURL, oldClient := cursorUserHomeDir, cursorUsageBaseURL, cursorUsageClient
	t.Cleanup(func() { cursorUserHomeDir, cursorUsageBaseURL, cursorUsageClient = oldHome, oldURL, oldClient })
	cursorUserHomeDir = func() (string, error) { return home, nil }
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	makeUsageDB(t, cursorUsageDBPath(runtime.GOOS, home), `"usage-test-secret"`)
}

type usageRoundTripper func(*http.Request) (*http.Response, error)

func (f usageRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCursorGetUsageHTTP(t *testing.T) {
	for _, tc := range []struct {
		name      string
		first     string
		status    int
		wantCalls int
		wantError string
	}{
		{"primary", `{"planUsage":{"apiPercentUsed":20,"autoPercentUsed":30}}`, 200, 1, ""},
		{"fallback", `{}`, 200, 2, ""},
		{"no quota", `{"bonusSpend":5}`, 200, 2, "no subscription quota"},
		{"unauthorized", `{}`, 401, 1, "login expired"},
		{"forbidden", `{}`, 403, 1, "login expired"},
		{"service error", `{}`, 500, 1, "HTTP 500"},
		{"redirect", `{}`, 302, 1, "HTTP 302"},
		{"invalid JSON", `usage-test-secret`, 200, 1, "invalid usage response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupCursorUsage(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				method := "GetCurrentPeriodUsage"
				if calls == 2 {
					method = "GetUsageLimitStatusAndActiveGrants"
				}
				if r.URL.Path != "/aiserver.v1.DashboardService/"+method || r.Method != "POST" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer usage-test-secret" || r.Header.Get("Connect-Protocol-Version") != "1" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("invalid headers")
				}
				raw, _ := io.ReadAll(r.Body)
				if string(raw) != "{}" {
					t.Error("invalid request body")
				}
				w.Header().Set("Location", serverRedirectTarget(r))
				w.WriteHeader(tc.status)
				body := tc.first
				if calls == 2 && tc.name == "fallback" {
					body = `{"activeGrants":[{"totalCents":100,"remainingCents":50}]}`
				}
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			cursorUsageBaseURL, cursorUsageClient = server.URL, server.Client()
			report, err := (&Agent{}).GetUsage(context.Background())
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "usage-test-secret") {
					t.Fatalf("error: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if report.Provider != "cursor" || len(report.Buckets) != 1 || report.Buckets[0].Name != "Cursor" {
					t.Fatalf("report: %+v", report)
				}
			}
			if calls != tc.wantCalls {
				t.Fatalf("calls=%d want %d", calls, tc.wantCalls)
			}
		})
	}
}

func serverRedirectTarget(r *http.Request) string { return "http://" + r.Host + "/redirected" }

func TestCursorUsageTransportErrorRedactsToken(t *testing.T) {
	setupCursorUsage(t)
	cursorUsageClient = &http.Client{Transport: usageRoundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("failure usage-test-secret") })}
	_, err := (&Agent{}).GetUsage(context.Background())
	if err == nil || strings.Contains(err.Error(), "usage-test-secret") {
		t.Fatalf("error: %v", err)
	}
}

func TestCursorUsageCancellation(t *testing.T) {
	setupCursorUsage(t)
	cursorUsageClient = &http.Client{Transport: usageRoundTripper(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 70*time.Second {
			t.Error("missing usage deadline")
		}
		return nil, context.Canceled
	})}
	_, err := (&Agent{}).GetUsage(context.Background())
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestCursorUsageDBPathWindowsFallback(t *testing.T) {
	t.Setenv("APPDATA", "")
	got := cursorUsageDBPath("windows", "/home/test")
	want := filepath.Join("/home/test", "AppData/Roaming/Cursor/User/globalStorage/state.vscdb")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
