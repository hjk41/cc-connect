# Feishu Markdown and Mermaid acceptance

Status: independently accepted and deployed to the local **codex-bot** on 2026-11-02.

Implementation: OpenCode, `opencode-go/deepseek-v4.1-flash`, variant `low`.
Codex independently reviewed the implementation,
added independent acceptance assertions, requested fixes, and handled deployment.

[Design](2026-11-02-feishu-mermaid-markdown.md) ·
[Implementation report](2026-11-02-feishu-mermaid-implementation.md)

## Behavior verified

- The reported `**中文句子。**NLB` becomes `**中文句子。** NLB` and produces a
  real goldmark strong node. Code and link destinations stay protected.
- Complete Mermaid fences render locally to PNG and use Feishu image uploads.
  Streaming remains asynchronous, jobs deduplicate, and cache/queue/work limits
  are bounded. Errors retain source without chat warnings or source in logs.
- Reply, Send, footer, rich-card and streaming paths are covered by tests.
  Complete/unfinished backtick and tilde code survives the whole sanitizer,
  including four-backtick markers, headings, blank lines, and image syntax.
- Chinese flowchart (1168×368) and sequence diagram (1364×620) were visually
  inspected. The initial clipping bug was reproduced, returned for correction,
  then retested with all borders visible.
- Invalid syntax and configuration directives fail safely. An actual external
  image-node fixture produced zero requests to the root's localhost listener.
- Independently forced cancellation after Chrome started, and while the launch
  promise was pending, left no live owned processes or profile directories.
  Final Node cleanup waits are 500 ms each, shorter than Go's 2 s escalation.

## Final independent checks

| Check | Result |
| --- | --- |
| `go test -p 4 ./...` | PASS |
| `go test -race ./platform/feishu -count=1` | PASS |
| `go build ./...` and final candidate build | PASS |
| `go vet ./...` | PASS |
| Real Chrome Go integration + forced timeout cleanup | PASS |
| Windows amd64 test-binary cross-compilation | PASS (compile only) |
| Independent full-sanitizer fallback tests | PASS |
| Go overlay removing normalization from outgoing cards | Expected regression failure |
| Installed renderer, launchd Node PATH, minimal environment | PASS |
| `git diff --check` | PASS |

No core source changed. Full-suite tests include the existing core CUJs.
Real runtime/browser acceptance was performed on macOS arm64; Windows runtime
behavior was not exercised.

## Production evidence

A single labeled acceptance card was sent through the running codex-bot Unix
API to the existing conversation. The original card JSON was read back from
Feishu: repaired strong boundary present, Mermaid source absent, one native
image key present. The uploaded image was downloaded and visually inspected:

- PNG: 1168×192, 12544 bytes.
- SHA-256: `7aa7e02a7e28384bf1565e8a96d30e0a34ba04cb0285d04f8a8df3a04f5c37bc`.
- Exact byte match to the independently tested installed renderer output: **yes**.

For original JSON, Feishu message-list/get APIs require
`card_msg_content_type=user_card_content`; the default projection returns a
legacy upgrade placeholder, which alone is not evidence of rendering failure.
[Official message-list documentation](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/list).

This verifies server-side card content and the actual uploaded bitmap. It does
not claim a screenshot of the user's Feishu client UI.

## Installed versions and deployment

- Binary: `v1.5.7-codex-modes.2+feishu-render.20261102`.
- Source HEAD: `f7959ce64e6a9481c211e93a75a808f3cc0c6d3d` plus reviewed local feature changes.
- Binary SHA-256: `0d8338cf3934a39f79963b8fb019179b29b179b482c0ba8a5f996e3941a47c1e`.
- Node: 26.12.1 (minimum 22.12), Chrome: 154.0.8037.98.
- Mermaid: 12.2.0; puppeteer-core: 25.16.0, exact lockfile.
- Renderer: `<runtime-dir>/renderers/<version>/cc-connect-mermaid`; fresh `npm ci --ignore-scripts --omit=dev`.
- codex-bot restarted after its current turn completed.
- Devin/main and dsh retained their original processes.
- No new service errors during acceptance; all three processes stable afterward.

Only codex-bot's Feishu options were added: `render_mermaid=true`, absolute
`mermaid_renderer`, `mermaid_timeout_secs=15`. Source/session history and other
bot configs were preserved. The shared binary was atomically replaced; only
codex-bot was restarted. No cloud resources were modified. Deployment validation preceded the Git
commit and push.

## Rollback and evidence

Private rollback directory: `<runtime-dir>/backups/feishu-render-<timestamp>`.
It contains the original executable/config and before/deployment manifests.
Restore `cc-connect` to the runtime `bin/cc-connect` and
`config.codex-bot.toml` to its original path, then restart only
`gui/501/com.cc-connect.codex-bot`.

Detailed logs, original card JSON and PNGs are archived privately with that
backup. No credentials or tenant tokens are included in the evidence export.
