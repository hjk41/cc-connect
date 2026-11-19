package acp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// writeFactoryAuth encrypts an auth envelope exactly like droid does:
// iv:tag:ciphertext base64 segments sealed with a base64 AES-256-GCM key.
func writeFactoryAuth(t *testing.T, dir, envelopeFile, keyFile, keyB64 string, store map[string]any) {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	plain, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("marshal store: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	ciphertext, tag := sealed[:len(sealed)-gcm.Overhead()], sealed[len(sealed)-gcm.Overhead():]
	env := base64.StdEncoding.EncodeToString(nonce) + ":" +
		base64.StdEncoding.EncodeToString(tag) + ":" +
		base64.StdEncoding.EncodeToString(ciphertext)
	if err := os.WriteFile(filepath.Join(dir, envelopeFile), []byte(env), 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
	if keyFile != "" {
		if err := os.WriteFile(filepath.Join(dir, keyFile), []byte(keyB64), 0o600); err != nil {
			t.Fatalf("write key: %v", err)
		}
	}
}

func fakeFactoryJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." +
		enc.EncodeToString(payload) + "." + enc.EncodeToString([]byte("sig"))
}

func testFactoryKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func withFactorySeams(t *testing.T, homeDir, keychainKey, apiBase string) {
	t.Helper()
	oldDir, oldRead, oldURL := factoryHomeDir, factoryKeychainRead, factoryAPIBaseURL
	factoryHomeDir = func() string { return homeDir }
	factoryKeychainRead = func() (string, error) {
		if keychainKey == "" {
			return "", fmt.Errorf("no keychain")
		}
		return keychainKey, nil
	}
	factoryAPIBaseURL = apiBase
	t.Cleanup(func() {
		factoryHomeDir, factoryKeychainRead, factoryAPIBaseURL = oldDir, oldRead, oldURL
	})
}

func TestGetUsage_NotSupportedForNonDroid(t *testing.T) {
	a := &Agent{cmd: "kimi"}
	if _, err := a.GetUsage(context.Background()); !errors.Is(err, core.ErrNotSupported) {
		t.Fatalf("expected ErrNotSupported, got %v", err)
	}
}

func TestGetUsage_Factory(t *testing.T) {
	key := testFactoryKey(t)
	dir := t.TempDir()
	tok := fakeFactoryJWT(t, map[string]any{
		"exp":    time.Now().Add(time.Hour).Unix(),
		"email":  "user@example.com",
		"org_id": "org_123",
		"sub":    "user_123",
	})
	writeFactoryAuth(t, dir, factoryKeychainEnvelopeFile, "", key,
		map[string]any{"access_token": tok, "refresh_token": "rt"})

	windowEnd := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != factoryBillingLimitsPath {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+tok {
			t.Errorf("unexpected auth header %q", got)
		}
		pct := 42
		remain := 3600
		fmt.Fprintf(w, `{"hasActiveSubscription":true,"overagePreference":"droidCore",
"extraUsageAllowed":true,"extraUsageBalanceCents":1250,
"limits":{"standard":{"fiveHour":{"usedPercent":%d,"windowEnd":%q,"secondsRemaining":%d},
"weekly":{"usedPercent":7,"windowEnd":null,"secondsRemaining":null},
"monthly":{"usedPercent":1,"windowEnd":null,"secondsRemaining":null}},
"core":{"fiveHour":{"usedPercent":0,"windowEnd":null,"secondsRemaining":null},
"weekly":{"usedPercent":0,"windowEnd":null,"secondsRemaining":null},
"monthly":{"usedPercent":0,"windowEnd":null,"secondsRemaining":null}}}}`, pct, windowEnd, remain)
	}))
	defer srv.Close()
	withFactorySeams(t, dir, key, srv.URL)

	a := &Agent{cmd: "/usr/local/bin/droid"}
	report, err := a.GetUsage(context.Background())
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if report.Email != "user@example.com" || report.AccountID != "org_123" {
		t.Fatalf("identity not mapped: %+v", report)
	}
	if len(report.Buckets) != 2 || report.Buckets[0].Name != "Standard usage" {
		t.Fatalf("unexpected buckets: %+v", report.Buckets)
	}
	w := report.Buckets[0].Windows[0]
	if w.UsedPercent != 42 || w.WindowSeconds != 18000 || w.ResetAfterSeconds != 3600 {
		t.Fatalf("unexpected 5h window: %+v", w)
	}
	if w.ResetAtUnix == 0 {
		t.Fatalf("expected ResetAtUnix from windowEnd")
	}
	if report.Credits == nil || report.Credits.Balance != "$12.50" || !report.Credits.HasCredits {
		t.Fatalf("unexpected credits: %+v", report.Credits)
	}
	if report.Buckets[1].Name != "Droid Core" {
		t.Fatalf("expected Droid Core bucket when overagePreference=droidCore")
	}
}

func TestGetUsage_FactoryFileStoreFallback(t *testing.T) {
	key := testFactoryKey(t)
	dir := t.TempDir()
	tok := fakeFactoryJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	// No keychain envelope; only the file store pair exists.
	writeFactoryAuth(t, dir, factoryFileEnvelope, factoryFileKey, key,
		map[string]any{"access_token": tok})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"hasActiveSubscription":false,"limits":{"standard":{},"core":{}}}`)
	}))
	defer srv.Close()
	withFactorySeams(t, dir, "", srv.URL) // keychain lookup fails -> file store

	a := &Agent{cmd: "droid"}
	report, err := a.GetUsage(context.Background())
	if err != nil {
		t.Fatalf("GetUsage via file store: %v", err)
	}
	if len(report.Buckets) != 1 {
		t.Fatalf("unexpected buckets: %+v", report.Buckets)
	}
}

func TestGetUsage_FactoryExpiredToken(t *testing.T) {
	key := testFactoryKey(t)
	dir := t.TempDir()
	tok := fakeFactoryJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	writeFactoryAuth(t, dir, factoryFileEnvelope, factoryFileKey, key,
		map[string]any{"access_token": tok})
	withFactorySeams(t, dir, "", "http://127.0.0.1:1")

	a := &Agent{cmd: "droid"}
	if _, err := a.GetUsage(context.Background()); err == nil {
		t.Fatal("expected expired-token error")
	}
}

func TestGetUsage_FactoryNoLogin(t *testing.T) {
	withFactorySeams(t, t.TempDir(), "", "http://127.0.0.1:1")
	a := &Agent{cmd: "droid"}
	if _, err := a.GetUsage(context.Background()); err == nil {
		t.Fatal("expected error when no login store exists")
	}
}

func TestGetUsage_FactoryHTTPError(t *testing.T) {
	key := testFactoryKey(t)
	dir := t.TempDir()
	tok := fakeFactoryJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	writeFactoryAuth(t, dir, factoryFileEnvelope, factoryFileKey, key,
		map[string]any{"access_token": tok})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	withFactorySeams(t, dir, "", srv.URL)

	a := &Agent{cmd: "droid"}
	if _, err := a.GetUsage(context.Background()); err == nil {
		t.Fatal("expected HTTP error propagation")
	}
}
