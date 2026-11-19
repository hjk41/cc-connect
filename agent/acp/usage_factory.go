package acp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

const (
	factoryBillingLimitsPath    = "/api/billing/limits"
	factoryUsageRequestTimeout  = 20 * time.Second
	factoryKeychainService      = "Factory CLI"
	factoryKeychainAccount      = "auth-encryption-key-security-cli"
	factoryKeychainEnvelopeFile = "auth.v2.loginkeychain"
	factoryFileEnvelope         = "auth.v2.file"
	factoryFileKey              = "auth.v2.key"
)

// Injectable seams for tests.
var (
	factoryAPIBaseURL   = "https://api.factory.ai"
	factoryHomeDir      = defaultFactoryHomeDir
	factoryKeychainRead = readFactoryKeychainKey
)

// GetUsage implements core.UsageReporter for Factory Droid agents. It reuses
// droid's own login store under ~/.factory (never logging the token), calls
// the Factory billing-limits API, and maps the rolling quota windows onto a
// UsageReport. Other ACP agents get core.ErrNotSupported so /usage keeps its
// generic "not supported" reply.
func (a *Agent) GetUsage(ctx context.Context) (*core.UsageReport, error) {
	if a == nil || !isFactoryDroidCmd(a.cmd) {
		return nil, core.ErrNotSupported
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cred, err := loadFactoryCredentials()
	if err != nil {
		return nil, err
	}
	if cred.expiresAt > 0 && time.Now().Unix() >= cred.expiresAt {
		return nil, fmt.Errorf("droid: stored Factory login has expired; run `droid` once to refresh it")
	}
	return fetchFactoryLimits(ctx, cred)
}

func isFactoryDroidCmd(cmd string) bool {
	base := filepath.Base(strings.TrimSpace(cmd))
	return strings.TrimSuffix(base, ".exe") == "droid"
}

func defaultFactoryHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".factory")
}

// factoryCredentials is the subset of droid's auth store we need.
type factoryCredentials struct {
	accessToken string
	email       string
	orgID       string
	userID      string
	expiresAt   int64
}

// loadFactoryCredentials decrypts droid's auth envelope. On macOS the active
// store is auth.v2.loginkeychain whose AES key lives in the login keychain;
// the auth.v2.file + auth.v2.key pair is the file-mode fallback used when the
// keychain is unavailable.
func loadFactoryCredentials() (*factoryCredentials, error) {
	dir := factoryHomeDir()
	if dir == "" {
		return nil, fmt.Errorf("droid: cannot locate home directory for the Factory login store")
	}
	if raw, err := os.ReadFile(filepath.Join(dir, factoryKeychainEnvelopeFile)); err == nil {
		if key, kerr := factoryKeychainRead(); kerr == nil && strings.TrimSpace(key) != "" {
			if cred, derr := decryptFactoryAuth(raw, key); derr == nil {
				return cred, nil
			}
		}
	}
	env, envErr := os.ReadFile(filepath.Join(dir, factoryFileEnvelope))
	key, keyErr := os.ReadFile(filepath.Join(dir, factoryFileKey))
	if envErr != nil || keyErr != nil {
		return nil, fmt.Errorf("droid: no usable Factory login store found; run `droid` to sign in")
	}
	return decryptFactoryAuth(env, string(key))
}

// readFactoryKeychainKey fetches the base64 AES key droid stores in the macOS
// login keychain. Only darwin ships the `security` tool; other platforms use
// the file store.
func readFactoryKeychainKey() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("keychain unavailable on %s", runtime.GOOS)
	}
	out, err := exec.Command("/usr/bin/security", "find-generic-password",
		"-s", factoryKeychainService, "-a", factoryKeychainAccount, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("droid: read Factory key from login keychain: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// decryptFactoryAuth unwraps an `iv:tag:ciphertext` base64 envelope with the
// supplied base64 AES-256-GCM key and extracts the access token plus the
// identity claims embedded in it.
func decryptFactoryAuth(raw []byte, keyB64 string) (*factoryCredentials, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyB64))
	if err != nil {
		return nil, fmt.Errorf("droid: decode Factory auth key: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(raw)), ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("droid: malformed Factory auth envelope")
	}
	nonce, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("droid: decode Factory auth nonce: %w", err)
	}
	tag, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("droid: decode Factory auth tag: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("droid: decode Factory auth payload: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("droid: init Factory auth cipher: %w", err)
	}
	// Droid seals with a 16-byte nonce; cipher.NewGCM would panic on it.
	gcm, err := cipher.NewGCMWithNonceSize(block, len(nonce))
	if err != nil {
		return nil, fmt.Errorf("droid: init Factory auth GCM: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, append(ciphertext, tag...), nil)
	if err != nil {
		return nil, fmt.Errorf("droid: decrypt Factory auth store: %w", err)
	}
	var store struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(plain, &store); err != nil {
		return nil, fmt.Errorf("droid: parse Factory auth store: %w", err)
	}
	if strings.TrimSpace(store.AccessToken) == "" {
		return nil, fmt.Errorf("droid: Factory auth store has no access token")
	}
	cred := &factoryCredentials{accessToken: store.AccessToken}
	var claims struct {
		Exp   int64  `json:"exp"`
		Email string `json:"email"`
		OrgID string `json:"org_id"`
		Sub   string `json:"sub"`
	}
	if segs := strings.Split(store.AccessToken, "."); len(segs) == 3 {
		if payload, derr := base64.RawURLEncoding.DecodeString(segs[1]); derr == nil {
			if json.Unmarshal(payload, &claims) == nil {
				cred.expiresAt = claims.Exp
				cred.email = claims.Email
				cred.orgID = claims.OrgID
				cred.userID = claims.Sub
			}
		}
	}
	return cred, nil
}

// factoryLimitsResponse mirrors GET /api/billing/limits.
type factoryLimitsResponse struct {
	HasActiveSubscription bool   `json:"hasActiveSubscription"`
	OveragePreference     string `json:"overagePreference"`
	ExtraUsageAllowed     bool   `json:"extraUsageAllowed"`
	ExtraUsageBalanceCent int64  `json:"extraUsageBalanceCents"`
	Limits                struct {
		Standard factoryLimitPool `json:"standard"`
		Core     factoryLimitPool `json:"core"`
	} `json:"limits"`
}

type factoryLimitPool struct {
	FiveHour factoryLimitWindow `json:"fiveHour"`
	Weekly   factoryLimitWindow `json:"weekly"`
	Monthly  factoryLimitWindow `json:"monthly"`
}

type factoryLimitWindow struct {
	UsedPercent      *int    `json:"usedPercent"`
	WindowEnd        *string `json:"windowEnd"`
	SecondsRemaining *int    `json:"secondsRemaining"`
}

func fetchFactoryLimits(ctx context.Context, cred *factoryCredentials) (*core.UsageReport, error) {
	ctx, cancel := context.WithTimeout(ctx, factoryUsageRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		factoryAPIBaseURL+factoryBillingLimitsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("droid: billing limits request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cred.accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: factoryUsageRequestTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("droid: billing limits request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("droid: read billing limits response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("droid: Factory login rejected (HTTP %d); run `droid` once to refresh it", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("droid: billing limits request failed: HTTP %d", resp.StatusCode)
	}
	var payload factoryLimitsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("droid: parse billing limits response: %w", err)
	}

	report := &core.UsageReport{
		Provider:  "factory",
		Email:     cred.email,
		AccountID: cred.orgID,
		UserID:    cred.userID,
	}
	if payload.HasActiveSubscription {
		report.Plan = "subscription"
	}
	report.Buckets = []core.UsageBucket{
		{Name: "Standard usage", Allowed: payload.HasActiveSubscription, Windows: factoryPoolWindows(payload.Limits.Standard)},
	}
	// The core pool is the droid-core overage quota; show it when the account
	// routes overage there or it carries real data.
	if payload.OveragePreference == "droidCore" || factoryPoolHasData(payload.Limits.Core) {
		report.Buckets = append(report.Buckets, core.UsageBucket{
			Name:    "Droid Core",
			Allowed: true,
			Windows: factoryPoolWindows(payload.Limits.Core),
		})
	}
	if payload.ExtraUsageAllowed || payload.ExtraUsageBalanceCent != 0 {
		report.Credits = &core.UsageCredits{
			HasCredits: payload.ExtraUsageAllowed,
			Balance:    fmt.Sprintf("$%.2f", float64(payload.ExtraUsageBalanceCent)/100),
		}
	}
	return report, nil
}

func factoryPoolHasData(p factoryLimitPool) bool {
	for _, w := range []factoryLimitWindow{p.FiveHour, p.Weekly, p.Monthly} {
		if w.UsedPercent != nil || w.WindowEnd != nil {
			return true
		}
	}
	return false
}

func factoryPoolWindows(p factoryLimitPool) []core.UsageWindow {
	const (
		fiveHourSeconds = 18000
		weeklySeconds   = 604800
		monthlySeconds  = 2592000
	)
	return []core.UsageWindow{
		factoryWindowToUsage(p.FiveHour, fiveHourSeconds),
		factoryWindowToUsage(p.Weekly, weeklySeconds),
		factoryWindowToUsage(p.Monthly, monthlySeconds),
	}
}

func factoryWindowToUsage(w factoryLimitWindow, seconds int) core.UsageWindow {
	out := core.UsageWindow{WindowSeconds: seconds}
	if w.UsedPercent != nil {
		out.UsedPercent = *w.UsedPercent
	}
	if w.SecondsRemaining != nil {
		out.ResetAfterSeconds = *w.SecondsRemaining
	}
	if w.WindowEnd != nil {
		if t, err := time.Parse(time.RFC3339, *w.WindowEnd); err == nil {
			out.ResetAtUnix = t.Unix()
			if out.ResetAfterSeconds == 0 {
				if rem := int(time.Until(t).Seconds()); rem > 0 {
					out.ResetAfterSeconds = rem
				}
			}
		}
	}
	return out
}
