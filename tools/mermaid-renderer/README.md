# cc-connect Mermaid renderer

Local-only Mermaid-to-PNG renderer used by the Feishu/Lark adapter. It never
contacts a public rendering service and never downloads a browser at render
time.

## Requirements

- Node.js 22.12+
- An already installed Chrome or Chromium
- Pinned dependencies (see `package.json` / `package-lock.json`):
  - `mermaid` 12.2.0
  - `puppeteer-core` 25.16.0

## Install

```bash
cd tools/mermaid-renderer
npm ci --no-audit --no-fund
chmod +x cc-connect-mermaid
```

Chrome discovery order:

1. `CC_MERMAID_CHROME`
2. `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`
3. `/Applications/Chromium.app/Contents/MacOS/Chromium`
4. Common Linux locations (`google-chrome`, `chromium`, `chromium-browser`,
   `/snap/bin/chromium`)

On macOS with the standard Chrome install, no extra environment is required.

## Usage

```bash
CC_MERMAID_CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  ./cc-connect-mermaid --input diagram.mmd --output diagram.png
```

The renderer accepts only `--input` and `--output`, reads the diagram from a
private file, and writes a PNG. It exits non-zero with a safe error category on
failure (never printing diagram source to stderr).

## cc-connect configuration

```toml
[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "..."
app_secret = "..."
# render_mermaid = true
# mermaid_renderer = "/absolute/path/to/tools/mermaid-renderer/cc-connect-mermaid"
# mermaid_timeout_secs = 15
```

`render_mermaid` defaults to true. A missing renderer does not prevent the bot
from starting; complete diagrams retain their original fenced source. Only
complete `mermaid` fences (backtick or tilde, case-insensitive label) are
rendered. At most four diagrams per reply are rendered, with a 32 KiB source
limit, a 10 MiB PNG limit, and a 4096x4096 pixel limit.

## Safety properties

- Mermaid runs with `securityLevel: 'strict'`; HTML labels, click handlers,
  animation, and remote assets are disabled.
- Configuration frontmatter and `%%{...}%%` init directives are rejected for v1.
- All page requests except the initial `about:blank` are aborted, so HTTP(S),
  file, image, CSS, and script fetches are denied.
- Chrome's sandbox and certificate validation are not disabled.
- The Go adapter passes a minimal allowlisted environment (no proxies or cloud
  credentials) and a private `TMPDIR`; the renderer runs in its own process
  group with graceful SIGTERM followed by bounded SIGKILL escalation.
- Diagram source is never echoed to production logs.

## Fixtures

`fixtures/` contains manual acceptance inputs:

- `chinese-flowchart.mmd` — Chinese flowchart with system-font labels
- `sequence.mmd` — Chinese sequence diagram
- `invalid.mmd` — invalid syntax must fail and leave no output
- `external-image.mmd` — malicious external image reference must not fetch
- `directive.mmd` — configuration directive must be rejected

Run the opt-in Go integration test (real Chrome required):

```bash
cd ../..
CC_MERMAID_REAL_TEST=1 go test ./platform/feishu -run TestMermaidRealRenderer -count=1 -v
```
