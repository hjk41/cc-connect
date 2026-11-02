package feishu

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	mermaidMaxSourceBytes  = 32 * 1024
	mermaidMaxDiagrams     = 4
	mermaidMaxPNGBytes     = 10 * 1024 * 1024
	mermaidMaxDimension    = 4096
	mermaidCacheMaxEntries = 128
	mermaidMaxPendingJobs  = 8
	mermaidDefaultRenderer = "cc-connect-mermaid"
	mermaidDefaultTimeout  = 15 * time.Second
)

var (
	errMermaidTimeout       = errors.New("mermaid render timed out")
	errMermaidMissing       = errors.New("mermaid renderer not found")
	errMermaidRenderFailed  = errors.New("mermaid render failed")
	errMermaidInvalidOutput = errors.New("mermaid renderer produced an invalid image")
	errMermaidStopped       = errors.New("mermaid renderer stopped")
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

type mermaidJob struct {
	key    string
	source string
	done   chan struct{}
}

type mermaidCacheEntry struct {
	imageKey string
	failed   bool
	element  *list.Element
}

// mermaidResolver owns the per-platform Mermaid render cache and a single
// sequential background worker. It is deliberately independent from Feishu
// upload code so the render/upload seam can be replaced in tests.
type mermaidResolver struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	cache   map[string]*mermaidCacheEntry
	order   *list.List
	pending map[string]*mermaidJob
	queue   []*mermaidJob
	running bool
	stopped bool
	wg      sync.WaitGroup

	render    func(context.Context, string) (string, error)
	settings  string
	finalWait time.Duration
}

func newMermaidResolver(settings string, timeout time.Duration, render func(context.Context, string) (string, error)) *mermaidResolver {
	ctx, cancel := context.WithCancel(context.Background())
	finalWait := timeout + 2*time.Second
	if finalWait < 4*time.Second {
		finalWait = 4 * time.Second
	}
	if finalWait > 20*time.Second {
		finalWait = 20 * time.Second
	}
	return &mermaidResolver{
		ctx:       ctx,
		cancel:    cancel,
		cache:     make(map[string]*mermaidCacheEntry),
		order:     list.New(),
		pending:   make(map[string]*mermaidJob),
		render:    render,
		settings:  settings,
		finalWait: finalWait,
	}
}

func (r *mermaidResolver) Resolve(ctx context.Context, markdown string, final bool) string {
	if markdown == "" || (!strings.Contains(markdown, "```") && !strings.Contains(markdown, "~~~")) {
		return markdown
	}
	fences := findMermaidFences(markdown)
	if len(fences) == 0 {
		return markdown
	}

	limit := len(fences)
	if limit > mermaidMaxDiagrams {
		limit = mermaidMaxDiagrams
	}

	var waits []*mermaidJob
	for i := 0; i < limit; i++ {
		source := fences[i].source
		if !validMermaidSource(source) {
			continue
		}
		key := r.cacheKey(source)

		r.mu.Lock()
		if _, ok := r.cache[key]; ok {
			r.mu.Unlock()
			continue
		}
		if job, ok := r.pending[key]; ok {
			waits = append(waits, job)
			r.mu.Unlock()
			continue
		}
		job, ok := r.enqueueLocked(source, key)
		r.mu.Unlock()
		if ok {
			waits = append(waits, job)
		}
	}

	if final && len(waits) > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, r.finalWait)
		for _, job := range waits {
			select {
			case <-job.done:
			case <-waitCtx.Done():
				cancel()
				return r.replaceFences(markdown, fences, limit)
			}
		}
		cancel()
	}
	return r.replaceFences(markdown, fences, limit)
}

func (r *mermaidResolver) replaceFences(markdown string, fences []mermaidFence, limit int) string {
	var b strings.Builder
	b.Grow(len(markdown))
	last := 0
	for i := 0; i < limit; i++ {
		fence := fences[i]
		if fence.start < last || fence.end > len(markdown) {
			continue
		}
		key := r.cacheKey(fence.source)
		r.mu.Lock()
		entry, ok := r.cache[key]
		if !ok || entry.failed || entry.imageKey == "" {
			r.mu.Unlock()
			continue
		}
		imageKey := entry.imageKey
		r.mu.Unlock()

		b.WriteString(markdown[last:fence.start])
		fmt.Fprintf(&b, "![Mermaid](%s)", imageKey)
		switch raw := markdown[fence.start:fence.end]; {
		case strings.HasSuffix(raw, "\r\n"):
			b.WriteString("\r\n")
		case strings.HasSuffix(raw, "\n"):
			b.WriteString("\n")
		}
		last = fence.end
	}
	b.WriteString(markdown[last:])
	return b.String()
}

func (r *mermaidResolver) cacheKey(source string) string {
	sum := sha256.Sum256([]byte(r.settings + "\x00" + source))
	return hex.EncodeToString(sum[:])
}

func validMermaidSource(source string) bool {
	if source == "" || len(source) > mermaidMaxSourceBytes {
		return false
	}
	return utf8.ValidString(source)
}

func (r *mermaidResolver) enqueueLocked(source, key string) (*mermaidJob, bool) {
	if r.stopped || len(r.pending) >= mermaidMaxPendingJobs {
		return nil, false
	}
	job := &mermaidJob{key: key, source: source, done: make(chan struct{})}
	r.pending[key] = job
	r.queue = append(r.queue, job)
	if !r.running {
		r.running = true
		r.wg.Add(1)
		go r.worker()
	}
	return job, true
}

func (r *mermaidResolver) worker() {
	defer r.wg.Done()
	for {
		r.mu.Lock()
		if len(r.queue) == 0 {
			r.running = false
			r.mu.Unlock()
			return
		}
		job := r.queue[0]
		r.queue = r.queue[1:]
		ctx := r.ctx
		render := r.render
		stopped := r.stopped
		r.mu.Unlock()

		var (
			imageKey string
			err      error
		)
		if stopped || render == nil {
			err = errMermaidStopped
		} else {
			imageKey, err = render(ctx, job.source)
		}
		r.finishJob(job, imageKey, err)
	}
}

func (r *mermaidResolver) finishJob(job *mermaidJob, imageKey string, err error) {
	r.mu.Lock()
	delete(r.pending, job.key)
	if err == nil && imageKey != "" {
		r.putCacheLocked(job.key, imageKey, false)
	} else {
		r.putCacheLocked(job.key, "", true)
		slog.Debug("feishu: mermaid render failed; keeping source", "category", safeMermaidError(err))
	}
	close(job.done)
	r.mu.Unlock()
}

func (r *mermaidResolver) putCacheLocked(key, imageKey string, failed bool) {
	if entry, ok := r.cache[key]; ok {
		entry.imageKey = imageKey
		entry.failed = failed
		return
	}
	entry := &mermaidCacheEntry{imageKey: imageKey, failed: failed}
	entry.element = r.order.PushBack(key)
	r.cache[key] = entry
	for len(r.cache) > mermaidCacheMaxEntries {
		front := r.order.Front()
		if front == nil {
			break
		}
		r.order.Remove(front)
		delete(r.cache, front.Value.(string))
	}
}

// Stop cancels pending and running render work. The bounded wait keeps platform
// shutdown responsive while the process-group cancellation tears down Chrome.
func (r *mermaidResolver) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	r.cancel()
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func safeMermaidError(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, errMermaidTimeout):
		return "timeout"
	case errors.Is(err, errMermaidMissing):
		return "renderer-missing"
	case errors.Is(err, errMermaidInvalidOutput):
		return "invalid-image"
	case errors.Is(err, errMermaidStopped):
		return "stopped"
	default:
		return "render-failed"
	}
}

// renderMermaidSource is the production render seam. Tests replace the whole
// function through Platform.mermaidRenderFunc.
func (p *Platform) renderMermaidSource(ctx context.Context, source string) (string, error) {
	if p.mermaidRenderFunc != nil {
		return p.mermaidRenderFunc(ctx, source)
	}
	return p.renderMermaidPNG(ctx, source)
}

func (p *Platform) renderMermaidPNG(ctx context.Context, source string) (string, error) {
	if !validMermaidSource(source) {
		return "", errMermaidInvalidOutput
	}
	renderer := strings.TrimSpace(p.mermaidRenderer)
	if renderer == "" {
		renderer = mermaidDefaultRenderer
	}
	timeout := p.mermaidTimeout
	if timeout <= 0 {
		timeout = mermaidDefaultTimeout
	}
	renderCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp(p.mermaidTempDirBase, "cc-connect-mermaid-")
	if err != nil {
		return "", errMermaidRenderFailed
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", errMermaidRenderFailed
	}

	inputPath := filepath.Join(dir, "diagram.mmd")
	outputPath := filepath.Join(dir, "diagram.png")
	if err := os.WriteFile(inputPath, []byte(source), 0o600); err != nil {
		return "", errMermaidRenderFailed
	}
	if err := os.Chmod(inputPath, 0o600); err != nil {
		return "", errMermaidRenderFailed
	}

	cmd := exec.CommandContext(renderCtx, renderer, "--input", inputPath, "--output", outputPath)
	cmd.Dir = dir
	cmd.Env = mermaidEnvWithTempDir(dir)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	// Renderer stderr can contain the diagram source. Never forward it.
	cmd.Stderr = io.Discard
	configureMermaidProcess(cmd)
	err = runMermaidCommand(renderCtx, cmd)
	if err != nil {
		if errors.Is(renderCtx.Err(), context.DeadlineExceeded) || errors.Is(renderCtx.Err(), context.Canceled) {
			return "", errMermaidTimeout
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			p.logMermaidMissingOnce(renderer)
			return "", errMermaidMissing
		}
		return "", errMermaidRenderFailed
	}

	info, err := os.Stat(outputPath)
	if err != nil || info.Size() <= 0 || info.Size() > mermaidMaxPNGBytes {
		return "", errMermaidInvalidOutput
	}
	if err := os.Chmod(outputPath, 0o600); err != nil {
		return "", errMermaidRenderFailed
	}
	data, err := os.ReadFile(outputPath)
	if err != nil || len(data) > mermaidMaxPNGBytes {
		return "", errMermaidInvalidOutput
	}
	if err := validateMermaidPNG(data); err != nil {
		return "", err
	}
	if p.mermaidUploadFunc != nil {
		return p.mermaidUploadFunc(ctx, data)
	}
	return p.uploadImageKey(ctx, data)
}

func (p *Platform) logMermaidMissingOnce(renderer string) {
	p.mermaidLogOnce.Do(func() {
		slog.Warn("feishu: mermaid renderer not found; keeping original fenced source",
			"renderer", renderer,
			"hint", "install tools/mermaid-renderer (npm ci) and configure mermaid_renderer")
	})
}

func validateMermaidPNG(data []byte) error {
	if len(data) < len(pngSignature) || !bytes.Equal(data[:len(pngSignature)], pngSignature) {
		return errMermaidInvalidOutput
	}
	if len(data) > mermaidMaxPNGBytes {
		return errMermaidInvalidOutput
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errMermaidInvalidOutput
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > mermaidMaxDimension || cfg.Height > mermaidMaxDimension {
		return errMermaidInvalidOutput
	}
	// Decode the whole image, not just the header: this rejects truncated or
	// corrupt IDAT payloads that still expose a plausible IHDR.
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return errMermaidInvalidOutput
	}
	bounds := img.Bounds()
	if bounds.Dx() != cfg.Width || bounds.Dy() != cfg.Height {
		return errMermaidInvalidOutput
	}
	return nil
}

// runMermaidCommand starts cmd, lets the renderer react to SIGTERM, and
// escalates to a process-group SIGKILL if it does not exit promptly.
func runMermaidCommand(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			mermaidSignalProcessGroup(cmd, false)
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
				return
			case <-timer.C:
				select {
				case <-done:
					return
				default:
				}
				mermaidSignalProcessGroup(cmd, true)
			}
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	return err
}

// mermaidSafeEnv passes only the variables Node, Chrome, and locale handling
// need. Cloud/provider credentials and proxy settings are deliberately absent.
func mermaidEnvWithTempDir(dir string) []string {
	base := mermaidSafeEnv()
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		switch key {
		case "TMPDIR", "TMP", "TEMP":
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "TMPDIR="+dir, "TMP="+dir, "TEMP="+dir)
	return out
}

func mermaidSafeEnv() []string {
	allowed := map[string]struct{}{
		"PATH":                    {},
		"HOME":                    {},
		"TMPDIR":                  {},
		"TMP":                     {},
		"TEMP":                    {},
		"LANG":                    {},
		"LC_ALL":                  {},
		"LC_CTYPE":                {},
		"TZ":                      {},
		"USER":                    {},
		"LOGNAME":                 {},
		"XDG_CONFIG_HOME":         {},
		"XDG_CACHE_HOME":          {},
		"CC_MERMAID_CHROME":       {},
		"__CF_USER_TEXT_ENCODING": {},
	}
	var env []string
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, ok := allowed[key]; ok {
			env = append(env, kv)
		}
	}
	return env
}
