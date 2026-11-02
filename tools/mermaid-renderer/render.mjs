import fs from 'node:fs/promises';
import fsSync from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import puppeteer from 'puppeteer-core';

const MAX_SOURCE_BYTES = 32 * 1024;
const MAX_CSS_DIMENSION = 2048; // at scale 2 this caps screenshots at 4096 px
const SCALE = 2;
const PADDING = 16;
// Two cleanup waits must finish before the Go supervisor's 2 s escalation.
const SHUTDOWN_GRACE_MS = 500;

const FONT_FAMILY =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", ' +
  '"Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", ' +
  '"Noto Sans CJK JP", sans-serif';

const MERMAID_CONFIG = {
  startOnLoad: false,
  securityLevel: 'strict',
  theme: 'default',
  logLevel: 'fatal',
  suppressErrorRendering: true,
  maxTextSize: MAX_SOURCE_BYTES,
  maxEdges: 2000,
  fontFamily: FONT_FAMILY,
  flowchart: { htmlLabels: false, useMaxWidth: false },
  sequence: { useMaxWidth: false, mirrorActors: false },
  gantt: { useMaxWidth: false },
  class: { useMaxWidth: false },
  state: { useMaxWidth: false },
  er: { useMaxWidth: false },
  journey: { useMaxWidth: false },
  mindmap: { useMaxWidth: false },
  timeline: { useMaxWidth: false },
  gitGraph: { useMaxWidth: false },
  requirement: { useMaxWidth: false },
  quadrantChart: { useMaxWidth: false },
  sankey: { useMaxWidth: false },
  xyChart: { useMaxWidth: false },
  block: { useMaxWidth: false },
  packet: { useMaxWidth: false },
};

export async function main(argv) {
  const args = parseArgs(argv);
  const source = await fs.readFile(args.input, 'utf8');
  if (Buffer.byteLength(source, 'utf8') > MAX_SOURCE_BYTES) {
    throw codedError('SOURCE_TOO_LARGE', 'source too large');
  }
  if (source.trimStart().startsWith('---') || source.includes('%%{')) {
    throw codedError('DIRECTIVE_REJECTED', 'configuration directives are not accepted');
  }

  const chrome = findChrome();
  const bundlePath = fileURLToPath(new URL('./node_modules/mermaid/dist/mermaid.min.js', import.meta.url));
  const bundle = await fs.readFile(bundlePath, 'utf8');

  let browser;
  let launchPromise;
  let profileDir;
  let stopping = false;
  let cleanupPromise;

  const shutdown = () => {
    if (cleanupPromise) {
      return cleanupPromise;
    }
    cleanupPromise = (async () => {
      stopping = true;
      if (!browser && launchPromise) {
        try {
          browser = await withTimeout(launchPromise, SHUTDOWN_GRACE_MS);
        } catch {
          // Launch may still be in flight; Puppeteer's own SIGTERM handler
          // also tears down any browser process it managed to start.
        }
      }
      if (browser) {
        const child = browser.process();
        try {
          await withTimeout(browser.close(), SHUTDOWN_GRACE_MS);
        } catch {
          // Escalate below.
        }
        if (child && child.pid) {
          try {
            process.kill(-child.pid, 'SIGKILL');
          } catch {
            try {
              child.kill('SIGKILL');
            } catch {
              // Already gone.
            }
          }
        }
      }
      if (profileDir) {
        try {
          await fs.rm(profileDir, { recursive: true, force: true });
        } catch {
          // The Go supervisor also removes its private TMPDIR on every path.
        }
      }
    })();
    return cleanupPromise;
  };

  const onSignal = () => {
    stopping = true;
    shutdown().finally(() => process.exit(1));
  };
  process.once('SIGTERM', onSignal);
  process.once('SIGINT', onSignal);

  try {
    profileDir = await fs.mkdtemp(path.join(os.tmpdir(), 'cc-connect-mermaid-profile-'));
    launchPromise = puppeteer.launch({
      executablePath: chrome,
      headless: true,
      userDataDir: profileDir,
      args: [
        '--disable-background-networking',
        '--disable-component-update',
        '--disable-default-apps',
        '--disable-extensions',
        '--disable-sync',
        '--disable-dev-shm-usage',
        '--no-first-run',
        '--no-default-browser-check',
        '--disable-features=Translate,MediaRouter',
        '--host-resolver-rules=MAP * ~NOTFOUND',
      ],
    });
    browser = await launchPromise;
    launchPromise = undefined;
    if (stopping) {
      throw codedError('RENDER_FAILED', 'cancelled during browser launch');
    }

    const page = await browser.newPage();
    await page.setRequestInterception(true);
    page.on('request', (request) => {
      // No graph may fetch HTTP(S), file, image, CSS, or script resources.
      // setContent itself does not require a network request; only the initial
      // about:blank document is allowed.
      if (request.url() === 'about:blank') {
        request.continue().catch(() => {});
      } else {
        request.abort().catch(() => {});
      }
    });
    page.on('dialog', (dialog) => {
      dialog.dismiss().catch(() => {});
    });

    await page.setContent(
      '<!doctype html><html><head><meta charset="utf-8">' +
        '<style>' +
        'html,body{margin:0;padding:0;background:#fff;color:#1f2328;}' +
        `body{font-family:${FONT_FAMILY};}` +
        '#container{display:inline-block;padding:' + PADDING + 'px;background:#fff;}' +
        '#container svg{max-width:none!important;display:block;}' +
        '*{animation:none!important;transition:none!important;}' +
        '</style></head><body><div id="container"></div></body></html>',
      { waitUntil: 'load' },
    );

    await page.addScriptTag({ content: bundle });
    await page.evaluate(
      async (diagramSource, config) => {
        if (!window.mermaid) {
          throw new Error('mermaid bundle did not load');
        }
        window.mermaid.initialize(config);
        const { svg } = await window.mermaid.render('ccConnectMermaidDiagram', diagramSource);
        document.getElementById('container').innerHTML = svg;
        if (document.fonts && document.fonts.ready) {
          await document.fonts.ready;
        }
      },
      source,
      MERMAID_CONFIG,
    );

    const dims = await page.evaluate(() => {
      const container = document.getElementById('container');
      const svg = container.querySelector('svg');
      if (!svg) {
        return { width: 0, height: 0 };
      }
      const rect = container.getBoundingClientRect();
      return {
        width: Math.ceil(Math.max(rect.width, container.scrollWidth)),
        height: Math.ceil(Math.max(rect.height, container.scrollHeight)),
      };
    });
    if (!Number.isFinite(dims.width) || !Number.isFinite(dims.height) ||
        dims.width <= 0 || dims.height <= 0) {
      throw codedError('RENDER_FAILED', 'invalid diagram dimensions');
    }
    if (dims.width > MAX_CSS_DIMENSION || dims.height > MAX_CSS_DIMENSION) {
      throw codedError('TOO_LARGE', 'diagram dimensions exceed limit');
    }

    await page.setViewport({
      width: dims.width,
      height: dims.height,
      deviceScaleFactor: SCALE,
    });
    await page.screenshot({
      path: args.output,
      type: 'png',
      omitBackground: false,
      captureBeyondViewport: false,
    });
  } catch (err) {
    if (stopping) {
      throw codedError('RENDER_FAILED', 'cancelled');
    }
    if (err && typeof err === 'object' && err.code && isSafeCode(err.code)) {
      throw err;
    }
    throw codedError('RENDER_FAILED', 'rendering failed');
  } finally {
    process.removeListener('SIGTERM', onSignal);
    process.removeListener('SIGINT', onSignal);
    await shutdown();
  }
}

function parseArgs(argv) {
  let input;
  let output;
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--input' && i + 1 < argv.length) {
      input = argv[++i];
    } else if (argv[i] === '--output' && i + 1 < argv.length) {
      output = argv[++i];
    } else {
      throw codedError('EINVAL', 'usage: cc-connect-mermaid --input <file> --output <file>');
    }
  }
  if (!input || !output) {
    throw codedError('EINVAL', 'usage: cc-connect-mermaid --input <file> --output <file>');
  }
  return { input, output };
}

function findChrome() {
  const candidates = [
    process.env.CC_MERMAID_CHROME,
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Chromium.app/Contents/MacOS/Chromium',
    '/usr/bin/google-chrome',
    '/usr/bin/google-chrome-stable',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
    '/snap/bin/chromium',
    path.join(process.env.HOME || '', '.cache/puppeteer/chrome'),
  ].filter(Boolean);
  for (const candidate of candidates) {
    try {
      fsSync.accessSync(candidate, fsSync.constants.X_OK);
      return candidate;
    } catch {
      // Try the next documented local location.
    }
  }
  throw codedError('BROWSER_MISSING', 'Chrome/Chromium not found; set CC_MERMAID_CHROME');
}

function withTimeout(promise, ms) {
  return Promise.race([
    promise,
    new Promise((_, reject) => setTimeout(() => reject(new Error('timeout')), ms)),
  ]);
}

function codedError(code, message) {
  const err = new Error(message);
  err.code = code;
  return err;
}

function isSafeCode(code) {
  return [
    'EINVAL',
    'ENOENT',
    'EACCES',
    'SOURCE_TOO_LARGE',
    'DIRECTIVE_REJECTED',
    'BROWSER_MISSING',
    'RENDER_FAILED',
    'TOO_LARGE',
  ].includes(code);
}
