# Feishu Markdown boundary repair and local Mermaid images

Status: implemented and tested by OpenCode using `deepseek-v4.1-flash`; independently accepted and deployed to codex-bot by Codex on 2026-11-02. See [final acceptance](2026-11-02-feishu-mermaid-acceptance.md).

## Problem and intended behavior

The model emits CommonMark-adjacent Chinese emphasis such as `**中文句子。**NLB`.
The closing delimiter is preceded by punctuation and followed by a letter, so a
CommonMark parser leaves literal asterisks. The current Feishu adapter forwards
that text without repairing the boundary. Also, fenced Mermaid is currently sent
as code: neither Feishu message Markdown nor cc-connect renders it as a diagram.

Deliver two independently tested adapter capabilities:

1. Conservatively repair paired strong-emphasis boundaries outside code so the
   original Chinese punctuation and words survive and the bold span renders.
2. Render complete Mermaid fences locally to PNG, upload through the existing
   Feishu image API, and replace only successfully rendered fences with
   `![Mermaid](img_...)`. Failed/disabled/oversized/incomplete diagrams
   retain their exact original source. Ordinary text diagrams are not guessed.

This change does not create cloud documents or use an external rendering service.
It must work across the normal Reply/Send, footer and rich streaming card paths.
Keep implementation in `platform/feishu/` and a standalone local renderer under
`tools/mermaid-renderer/`; do not add platform-specific logic to core.

## Markdown normalization

Implement a pure, idempotent helper. Recognize paired, unescaped `**` delimiters;
repair the demonstrated punctuation/letter boundary by inserting a separating
space after the intended closer. If implementing the symmetric opener boundary,
keep that equally conservative and explicitly tested. Do not blindly replace all
asterisks or use a single regex over the entire document.

Preserve fenced code (backtick and tilde fences, including unfinished fences),
inline code, escaped markers, triple emphasis and already valid Markdown. Avoid
changing link destinations or image destinations. Preserve sentence punctuation.
Normalization must be shared by card creation/final replacement and the direct
streaming-element update, not only one send path. Use goldmark in regression
tests to demonstrate that the reported example produces a strong node after
repair and fails that assertion on pre-fix code. The output must normalize to
itself when called twice.

## Local renderer contract

Ship an executable `tools/mermaid-renderer/cc-connect-mermaid` launching a Node
module without a shell command interpolated from diagram text. Use exact npm
versions and a lockfile for Mermaid and `puppeteer-core`; use an already installed
Chrome/Chromium selected through `CC_MERMAID_CHROME` or documented local discovery.
Do not download a browser from the rendering process. An explicit install/setup
step may install dependencies; rendering must never invoke npm/npx or the network.

Contract: `cc-connect-mermaid --input <private.mmd> --output <private.png>`.
The Go adapter invokes the executable with argv and `exec.CommandContext`, never
`sh -c`. Source is a private file, not an argv string. Use a 0700 temporary
directory, 0600 input/output where feasible, and remove it on every success and
failure. Validate real PNG signature, decode dimensions, and bound output before
upload. Renderer stderr must not be echoed to production logs because Mermaid
errors can contain the diagram source. Emit safe error categories only.

Render in a fresh, locally supplied page with bundled Mermaid JS, a white
background, readable system fonts including Chinese, and deviceScaleFactor 2.
Use Mermaid `securityLevel: strict`, disable click/HTML features and animation,
and disable remote fonts/assets. Intercept and deny network requests; no graph
may fetch HTTP(S), file resources, images, CSS or execute user JavaScript. Deny
configuration directives/frontmatter that could weaken these guarantees (simple
rejection with source fallback is acceptable for v1). Do not disable Chromium's
sandbox or certificate validation. Never pass inherited cloud/provider secrets
to the renderer: use an allowlisted minimal environment required for Node,
Chrome and locale, with proxies and credential variables absent.

Runtime bounds: at most 32 KiB UTF-8 Mermaid source; at most 4 diagrams per reply;
at most 10 MiB PNG and 4096x4096 pixels. Reject or scale safely before screenshot.
All renderer and browser children must be torn down on timeout/cancellation;
ensure terminating Node cannot leave Chrome processes behind. Use signal cleanup
plus a process group on Unix (portable build tags for other platforms).

## Configuration and asynchronous pipeline

Feishu/Lark factory options:

- `render_mermaid`: boolean, default true; best effort with original-source fallback.
- `mermaid_renderer`: executable path or name, default `cc-connect-mermaid` on PATH.
- `mermaid_timeout_secs`: default 15, validate a sensible bounded range (1..60).

No binary on PATH must not prevent the bot starting or ordinary replies working.
Log the missing renderer once, without diagram source, and document installation.
Do not add unsolicited chat warnings when rendering fails; preserve the source.

Recognize complete CommonMark-style `mermaid` fences (backticks or tildes,
case-insensitive info label, same closing marker and sufficient closing length).
Do not match Mermaid-looking text inside another fenced code block. Preserve
unclosed fences until a complete fence arrives. Preserve multiple-diagram order.

Extend the existing RichCardMarkdownResolver pipeline, retaining the current
remote-image behavior. Streaming calls start bounded background work and return
promptly with original code until the image is ready. Once ready replace the
matching fence in a subsequent frame. Final calls wait within a total bounded
budget, then keep original source for anything unresolved. Do not rely on a
new streamed token arriving to make the final image visible.

Deduplicate by source hash plus renderer settings. Cache success image keys and
failure outcomes with a bounded LRU/FIFO of at most 128 entries and a bounded
pending queue (8 jobs, one running local renderer per platform). A full queue
must fall back immediately. Protect all state against concurrent updates. Failed
items must not be relaunched for every streamed token. Platform Stop cancels
pending/running work and does not leak goroutines, processes or temp files.

The ordinary Reply/Send/footer path must also await this resolver for complete
diagrams. Keep resolved @mention messages in their existing text-message path;
do not replace Mermaid with image syntax when the result will be plain text.
Preserve already-uploaded img_ references and remote-image SSRF protections.

## Acceptance and tests

OpenCode must implement tests and run them before returning the work. It must
not deploy, modify ~/.cc-connect, change launchd, send messages, or stop bots.

Required regression coverage:

- Reported Chinese punctuation + `**NLB` example, with real goldmark parse;
  already-valid strong spans, Chinese quotes if supported, multiple spans,
  escapes, inline/fenced/unclosed code, links and idempotence.
- Complete/incomplete and nested/code fence recognition; backtick/tilde fences;
  multiple diagrams retain surrounding text and order; disabled/missing binary.
- Mock render/upload integration: image key appears in the final card; Reply,
  Send/footer and rich streaming paths; ordinary/code-only text unchanged.
- Streaming returns promptly while worker is blocked, only one render/upload
  per source under concurrent calls, final waits boundedly, errors retain source,
  queue/cache limits and Stop/cancellation cleanup. Run package race detector.
- Renderer exit errors, timeout, malformed/oversized PNG and excessive dimensions,
  no source in logs/errors, safe argv/environment and temporary-file cleanup.
- Real local renderer integration: Chinese flowchart, sequence diagram and invalid
  syntax. Prove outbound requests are blocked for malicious external references
  and rejected directives; do not use public renderers. Check actual screenshots.

Root independently reviews the diff and reruns appropriate tests, `go build ./...`,
`go vet ./...`, and `go test ./...`. Run `go test ./core -run TestCUJ` if core/engine
or user journeys change; avoid core edits if the existing resolver suffices.
Save a concise acceptance report and the exact renderer/package/binary versions.

## Local rollout and rollback

User authorized local deployment and a minimal validation message to the existing
codex-bot conversation as part of acceptance. Do not send notifications elsewhere.
First install renderer dependencies and render/inspect standalone fixtures.
Back up the running binary and codex-bot config, build a candidate from reviewed
source, and configure only codex-bot with the renderer's absolute path. All launchd
services share the binary path: atomically replace it but restart only
`com.cc-connect.codex-bot`, after checking for an active turn. Do not restart Devin
or dsh. Keep their current processes alive. Verify codex-bot reconnects, resumes
normal operation, sends a real uploaded PNG in a card, and retains normal text.

If rendering/upload or service health fails, restore the old binary/config and
restart only codex-bot. Keep conversation/session files. Record any verification
that cannot be performed rather than claiming visual success from a unit test.

## Sources

- CommonMark emphasis rules: https://spec.commonmark.org/0.31.2/#emphasis-and-strong-emphasis
- Mermaid integration: https://mermaid.js.org/config/usage.html
- Mermaid configuration/security: https://mermaid.js.org/config/configuration
- Feishu message formats: https://open.feishu.cn/document/server-docs/im-v1/message-content-description/create_json
