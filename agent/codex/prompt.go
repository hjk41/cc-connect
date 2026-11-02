package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/chenhg5/cc-connect/core"
)

// codexAttachmentGuidance is appended to the native developer instructions so
// Codex knows that cc-connect turns output citations into real attachments.
// Without this, the model falls back to whatever chat CLI it can find on PATH
// (e.g. lark-cli), which posts from a different Feishu app and makes
// cc-connect ingest the file again as an inbound user upload.
const codexAttachmentGuidance = `### Delivering generated files
When you create or modify a local file (deck, document, spreadsheet, PDF, image, archive), include the exact Codex file-citation marker for it in your final response, for example:

:codex-file-citation{path="/absolute/path/report.pdf" purpose="output"}

cc-connect automatically uploads every purpose="output" citation from your final response as an attachment in the current chat, using this bot's own identity. Do NOT use lark-cli, a webhook, or another messaging CLI to send those files: that posts from a different app and cc-connect receives the file again as a user upload.

For a generated file that cannot be expressed as a citation, use:

  cc-connect send --file /absolute/path/to/file

Do not use that command for ordinary text replies. cc-connect sets CC_PROJECT and the unfiltered CC_SESSION variable in your shell environment, so you normally do not need --project/--session; pass them only if cc-connect send reports that the session is ambiguous.`

// buildCodexDeveloperPrompt returns the developer instructions injected into
// every Codex session: the operator's existing developer_instructions (if any),
// the cc-connect tool prompt, and Codex-specific attachment guidance.
func buildCodexDeveloperPrompt(lang core.Language, existing string) string {
	parts := make([]string, 0, 3)
	if existing = strings.TrimSpace(existing); existing != "" {
		parts = append(parts, existing)
	}
	parts = append(parts, core.AgentSystemPromptForLang(lang), codexAttachmentGuidance)
	return strings.Join(parts, "\n\n")
}

// readConfiguredDeveloperInstructions reads the user's top-level
// developer_instructions from Codex's config.toml. The -c flag used to inject
// the cc-connect prompt overrides that key, so preserving the user's value is
// required to avoid silently clobbering their configuration.
func readConfiguredDeveloperInstructions(codexHome string) string {
	home := strings.TrimSpace(codexHome)
	if home == "" {
		if resolved, err := resolveCodexHome(nil); err == nil {
			home = resolved
		}
	}
	if home == "" {
		return ""
	}
	path := filepath.Join(home, "config.toml")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	var cfg struct {
		DeveloperInstructions string `toml:"developer_instructions"`
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.DeveloperInstructions)
}

// codexDeveloperArgs returns the -c pair that sets developer instructions.
// Codex parses -c values as TOML, so the prompt is encoded as a TOML basic
// string (JSON string escaping is a compatible subset).
func codexDeveloperArgs(prompt string) []string {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	return []string{"-c", "developer_instructions=" + tomlBasicString(prompt)}
}

// tomlBasicString encodes s as a TOML basic string. JSON escaping (\n, \t, \",
// \\, \uXXXX) is a compatible subset of TOML's basic-string escapes.
func tomlBasicString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
