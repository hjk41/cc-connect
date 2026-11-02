# Feishu Mermaid + markdown implementation report

Status: implementation worker complete; root accepted and deployed. See
[final acceptance](2026-11-02-feishu-mermaid-acceptance.md).
Model: `opencode-go/deepseek-v4.1-flash` (variant low), no delegation.

## Scope delivered

- Conservative, idempotent repair of paired `**` strong-emphasis boundaries
  (reported `**中文句子。**NLB` case plus symmetric opener handling).
- Local-only Mermaid-to-PNG renderer with pinned dependencies, request denial,
  strict Mermaid security, process-group cleanup, and Feishu upload.
- Asynchronous resolver integrated into rich-card, streaming, ordinary
  Reply/Send, footer, preview, and update paths.
- Config options, README, fixtures, and regression/concurrency tests.
- No deployment, home config edits, service changes, commits, pushes, or chat
  messages.

## Changed files

Go adapter:

- `platform/feishu/markdown_normalize.go` (new)
- `platform/feishu/mermaid.go` (new)
- `platform/feishu/mermaid_fence.go` (new)
- `platform/feishu/mermaid_proc_unix.go` (new)
- `platform/feishu/mermaid_proc_other.go` (new)
- `platform/feishu/feishu.go` (integration)
- `go.mod` (`goldmark` promoted to a direct test dependency)

Tests:

- `platform/feishu/markdown_normalize_test.go` (new)
- `platform/feishu/mermaid_test.go` (new)
- `platform/feishu/mermaid_integration_test.go` (new)
- `platform/feishu/mermaid_real_test.go` (new, opt-in)
- `platform/feishu/mermaid_acceptance_test.go` (acceptance regression tests
  present in the workspace; retained and passing)

Renderer/docs/config:

- `tools/mermaid-renderer/cc-connect-mermaid` (new executable)
- `tools/mermaid-renderer/render.mjs` (new)
- `tools/mermaid-renderer/package.json` (new)
- `tools/mermaid-renderer/package-lock.json` (new; unignored for this path)
- `tools/mermaid-renderer/README.md` (new)
- `tools/mermaid-renderer/fixtures/*.mmd` (new)
- `config.example.toml` (three Feishu/Lark options)
- `.gitignore` (allow the renderer lockfile)

## Review fixes

1. **Link/autolink destinations:** `normalizeFeishuMarkdown` now protects
   reference definitions (`[x]: https://…`) and angle/mailto autolinks in
   addition to inline link/image destinations. Tests assert exact unchanged
   destinations while the reported bold repair still works.
2. **Screenshot cropping:** `render.mjs` measures the full `#container`
   bounding box (including its 32 px padding), uses `ceil`, and rejects any
   total CSS dimension above 2048 so scale 2 cannot exceed 4096 px per side.
   Re-rendered outputs have visible right/bottom padding.
3. **Timeout cleanup:** Go now sends SIGTERM to the renderer process group first
   and escalates to process-group SIGKILL after 2 s. Node handles SIGTERM,
   awaits/closes its Puppeteer browser with a bounded timeout, kills the Chrome
   group if needed, and removes its profile directory. The Go adapter points
   `TMPDIR`/`TMP`/`TEMP` at its private per-render directory, removed on every
   path. An opt-in real-Chrome test proves no owned Chrome/renderer process or
   profile/temp directory survives a forced timeout.
4. **Fenced-source privacy/fallback:** remote-image upload, invalid-image
   stripping, URL sanitization, table detection/downgrade, heading compaction,
   and blank-line compaction now all preserve complete and unfinished backtick
   and tilde fences. A failed/incomplete Mermaid fence containing
   `A["![x](https://example.com/a.png)"]` triggers no upload and survives card
   sanitation byte-for-byte.
5. **PNG validation:** `validateMermaidPNG` fully decodes the image (not just
   IHDR) and verifies decoded bounds, rejecting truncated/corrupt payloads.
   Missing absolute renderer paths (`os.ErrNotExist`) are classified as
   `renderer-missing` and warned about once without source.
6. **Pre-existing fence edge case:** `preprocessFeishuMarkdown` no longer
   inserts newlines inside four-backtick markers, preserving fallback source.

## Test results (actual)

Commands were run with their true exit status; no pipes hid failures.

| Command | Result |
| --- | --- |
| `go build ./...` | PASS |
| `go vet ./...` | PASS (exit 0) |
| `go test ./platform/feishu -count=1` | PASS (35.8 s, exit 0) |
| `go test ./platform/feishu -race -count=1` | PASS (37.3 s, exit 0) |
| `go test ./...` | PASS (all packages, exit 0) |
| `CC_MERMAID_REAL_TEST=1 go test ./platform/feishu -run TestMermaidRealRenderer -count=1 -v` | PASS (integration 6.55 s; timeout cleanup 1.56 s) |

Notable covered cases: real goldmark strong-node assertion for the reported
example (pre-fix 0 strong nodes, post-fix 1); valid/escaped/triple/unpaired
emphasis; inline/fenced/unclosed code; links/images/reference definitions/
autolinks; idempotence; complete/incomplete/nested fences; backtick/tilde and
long markers; multiple diagrams and the four-diagram cap; disabled/missing
renderer; Reply/Send/footer/rich-card/streaming-element paths; plain-text
@mention preservation; streaming promptness and deduplication; bounded final
wait; queue/cache bounds; failure caching; Stop cancellation; malformed,
truncated, oversized PNG; missing absolute renderer; argv/temp cleanup.

## Real renderer fixtures

Inputs (committed): `tools/mermaid-renderer/fixtures/`

- `chinese-flowchart.mmd` → PASS, PNG `784 x 968`
- `sequence.mmd` → PASS, PNG `1364 x 652`
- `invalid.mmd` → expected exit 1 (`render-failed`)
- `directive.mmd` → expected exit 1 (`directive-rejected`)
- `external-image.mmd` → PASS, PNG `400 x 352`

Visual inspection of the Chinese flowchart and sequence screenshots confirmed
readable Chinese system fonts and visible right/bottom padding. A local HTTP
listener on `127.0.0.1:18765` received **zero** requests while rendering
`external-image.mmd`; the renderer exited 0.

## Versions and environment

- Go: `go1.27.2 darwin/arm64`
- Node: `v26.12.1`
- npm: `11.20.0`
- Mermaid: `12.2.0` (exact)
- puppeteer-core: `25.16.0` (exact; lockfile present)
- Chrome:
  `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`
- npm cache: isolated temporary directory

## Limitations

- v1 rejects Mermaid YAML frontmatter and `%%{...}%%` init directives and keeps
  the original source. This intentionally avoids diagrams that weaken the
  sandbox/security configuration.
- Rendering requires a pre-installed Chrome/Chromium and Node 22.12+; a missing
  renderer/browser only logs once and falls back to source.
- At most four diagrams per reply, 32 KiB source each, 10 MiB PNG, and
  4096×4096 pixels; oversized diagrams retain source.
- Real-renderer Go tests are opt-in (`CC_MERMAID_REAL_TEST=1`) because Chrome is
  environment-specific.
- No deployment, launchd changes, bot restarts, or validation messages were
  performed, by instruction.

## Root acceptance refinements

Root corrected the minimum Node version to 22.12, bounded each Node cleanup
wait to 500 ms, and used the proper-name `Mermaid` as the image alt label.
Final full/race/real-renderer checks passed again; see the acceptance report.
