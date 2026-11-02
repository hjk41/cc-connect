package codex

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// codexFileCitationRe matches Codex's artifact reference markup, for example:
//
//	:codex-file-citation{path="/abs/deck.pptx" purpose="output"}
//
// The artifact skills (presentations, spreadsheets, pdf, docs) require the
// model to emit these markers in the final response for every file it created
// or used. Codex's own clients turn them into download chips; cc-connect has
// to turn purpose="output" markers into real platform attachments.
var codexFileCitationRe = regexp.MustCompile(`:codex-file-citation\{([^}]*)\}`)

// codexCitationAttrRe extracts key="value" pairs from a citation body. Paths
// may contain spaces and commas, so splitting on "," would be wrong.
var codexCitationAttrRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_-]*)="([^"]*)"`)

type codexFileCitation struct {
	Path    string
	Purpose string
}

// stripCodexFileCitations replaces every citation marker with the referenced
// file's basename and returns the parsed citations. The basename keeps the
// user-visible text meaningful (Codex's skills use the marker in place of the
// filename), while the marker syntax itself is internal.
func stripCodexFileCitations(text string) (string, []codexFileCitation) {
	if !strings.Contains(text, ":codex-file-citation{") {
		return text, nil
	}

	var citations []codexFileCitation
	clean := codexFileCitationRe.ReplaceAllStringFunc(text, func(marker string) string {
		c := parseCodexFileCitation(marker)
		if c.Path == "" {
			return ""
		}
		citations = append(citations, c)
		base := filepath.Base(c.Path)
		if base == "" || base == "." || base == string(filepath.Separator) {
			return ""
		}
		return "`" + strings.ReplaceAll(base, "`", "'") + "`"
	})
	return tidyCitationText(clean), citations
}

func parseCodexFileCitation(marker string) codexFileCitation {
	var c codexFileCitation
	for _, match := range codexCitationAttrRe.FindAllStringSubmatch(marker, -1) {
		switch strings.ToLower(match[1]) {
		case "path":
			c.Path = strings.TrimSpace(match[2])
		case "purpose":
			c.Purpose = strings.ToLower(strings.TrimSpace(match[2]))
		}
	}
	return c
}

// tidyCitationText removes the whitespace artifacts left behind when a marker
// that sat on its own line is replaced with a short basename.
func tidyCitationText(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	text = strings.Join(lines, "\n")
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}

// codexOutputAttachments returns the stripped text plus the absolute paths of
// every purpose="output" citation that resolves to a regular file inside
// workDir. Restricting to the workspace prevents a model-authored citation
// from uploading arbitrary readable files (e.g. /etc/passwd or ~/.ssh keys).
func codexOutputAttachments(text, workDir string) (string, []string) {
	clean, citations := stripCodexFileCitations(text)
	if len(citations) == 0 {
		return clean, nil
	}

	root, err := filepath.Abs(workDir)
	if err != nil || workDir == "" {
		root, err = os.Getwd()
		if err != nil {
			return clean, nil
		}
	}

	seen := make(map[string]struct{}, len(citations))
	var paths []string
	for _, c := range citations {
		if c.Purpose != "output" || c.Path == "" {
			continue
		}
		path := c.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		resolved, ok := citationPathWithin(root, path)
		if !ok {
			continue
		}
		if _, dup := seen[resolved]; dup {
			continue
		}
		seen[resolved] = struct{}{}
		paths = append(paths, resolved)
	}
	return clean, paths
}

// citationPathWithin resolves root and path through symlinks and reports
// whether path is a regular file inside root.
func citationPathWithin(root, path string) (string, bool) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(realPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return realPath, true
}

// dedupeAttachmentPaths preserves order while removing duplicate paths. The
// app-server can deliver the same final agentMessage more than once, and a
// multi-part answer may cite the same artifact in several chunks.
func dedupeAttachmentPaths(paths []string) []string {
	if len(paths) < 2 {
		return paths
	}
	seen := make(map[string]struct{}, len(paths))
	out := paths[:0]
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
