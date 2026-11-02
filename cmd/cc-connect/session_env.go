package main

import (
	"os"
	"strings"
)

// sessionKeyFromEnv returns the session key injected by cc-connect into the
// agent process.
//
// CC_SESSION_KEY is the historical name. Codex's default shell environment
// policy filters variables matching *KEY* (and *TOKEN*), so a Codex agent's
// shell only sees the CC_SESSION alias added in core/engine.go. Reading both
// keeps every agent working without requiring callers to pass --session.
func sessionKeyFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("CC_SESSION_KEY")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("CC_SESSION"))
}
