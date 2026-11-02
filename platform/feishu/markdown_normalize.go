package feishu

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// normalizeFeishuMarkdown performs a deliberately narrow repair for a Strong
// emphasis boundary that CommonMark refuses to parse. Chinese text commonly
// ends a bold sentence with punctuation immediately before the closing "**",
// followed by a letter with no separating space:
//
//	**中文句子。**NLB
//
// The closing delimiter is preceded by punctuation and followed by a letter,
// so it is not right-flanking and the asterisks stay literal. Inserting a
// single space after the intended closer makes the span valid without touching
// the punctuation or the words.
//
// The helper is pure and idempotent. It ignores fenced/inline code, escaped
// markers, triple emphasis, and link/image destinations. It intentionally does
// not attempt to reimplement all of CommonMark emphasis; only paired,
// unescaped ** runs are considered.
func normalizeFeishuMarkdown(md string) string {
	if md == "" || !strings.Contains(md, "**") {
		return md
	}

	protected := protectedMarkdownRanges(md)
	runs := findStrongEmphasisRuns(md, protected)
	if len(runs) < 2 {
		return md
	}

	type insertion struct {
		pos int
	}
	var insertions []insertion
	var open *mdRange
	for i := range runs {
		run := runs[i]
		if open == nil {
			open = &runs[i]
			continue
		}
		if hasBlankLine(md[open.end:run.start]) {
			// Strong emphasis cannot cross a blank line; drop the stale opener.
			open = &runs[i]
			continue
		}
		openRun := *open
		closeRun := run
		open = nil

		// Symmetric opener repair: a "**" immediately after a letter and
		// immediately before punctuation is not left-flanking.
		prev, prevOK := runeBefore(md, openRun.start)
		next, nextOK := runeAt(md, openRun.end)
		if prevOK && nextOK &&
			!unicode.IsSpace(prev) && !unicode.IsPunct(prev) &&
			unicode.IsPunct(next) {
			insertions = append(insertions, insertion{pos: openRun.start})
		}

		// Demonstrated closer repair: punctuation before, letter/number after.
		prev, prevOK = runeBefore(md, closeRun.start)
		next, nextOK = runeAt(md, closeRun.end)
		if prevOK && nextOK &&
			unicode.IsPunct(prev) &&
			!unicode.IsSpace(next) && !unicode.IsPunct(next) {
			insertions = append(insertions, insertion{pos: closeRun.end})
		}
	}

	if len(insertions) == 0 {
		return md
	}

	sort.SliceStable(insertions, func(i, j int) bool {
		return insertions[i].pos < insertions[j].pos
	})

	var b strings.Builder
	b.Grow(len(md) + len(insertions))
	last := 0
	for _, ins := range insertions {
		if ins.pos < last || ins.pos < 0 || ins.pos > len(md) {
			continue
		}
		b.WriteString(md[last:ins.pos])
		b.WriteByte(' ')
		last = ins.pos
	}
	b.WriteString(md[last:])
	return b.String()
}

type mdRange struct {
	start int
	end   int
}

func protectedMarkdownRanges(md string) []mdRange {
	ranges := append([]mdRange(nil), fencedCodeRanges(md)...)
	ranges = append(ranges, inlineCodeRanges(md, ranges)...)
	ranges = append(ranges, linkDestinationRanges(md, ranges)...)
	ranges = append(ranges, referenceDefinitionRanges(md, ranges)...)
	ranges = append(ranges, angleAutolinkRanges(md, ranges)...)
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start == ranges[j].start {
			return ranges[i].end < ranges[j].end
		}
		return ranges[i].start < ranges[j].start
	})
	return ranges
}

// fencedCodeRanges returns every fenced code block, including an unfinished
// final fence, for both backtick and tilde fences.
func fencedCodeRanges(md string) []mdRange {
	var ranges []mdRange
	var fenceChar byte
	var fenceLen int
	var fenceStart int
	inFence := false

	for offset := 0; offset < len(md); {
		line, next := markdownLineAt(md, offset)
		if !inFence {
			if ch, n, ok := openingFenceLine(line); ok {
				inFence = true
				fenceChar = ch
				fenceLen = n
				fenceStart = offset
			}
		} else if closingFenceLine(line, fenceChar, fenceLen) {
			ranges = append(ranges, mdRange{start: fenceStart, end: next})
			inFence = false
		}
		offset = next
	}
	if inFence {
		ranges = append(ranges, mdRange{start: fenceStart, end: len(md)})
	}
	return ranges
}

func markdownLineAt(md string, offset int) (line string, next int) {
	if offset >= len(md) {
		return "", len(md)
	}
	rel := strings.IndexByte(md[offset:], '\n')
	if rel < 0 {
		return md[offset:], len(md)
	}
	end := offset + rel
	return md[offset:end], end + 1
}

func openingFenceLine(line string) (byte, int, bool) {
	i := skipFenceIndent(line)
	if i >= len(line) {
		return 0, 0, false
	}
	ch := line[i]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	j := i
	for j < len(line) && line[j] == ch {
		j++
	}
	n := j - i
	if n < 3 {
		return 0, 0, false
	}
	if ch == '`' && strings.ContainsRune(line[j:], '`') {
		return 0, 0, false
	}
	return ch, n, true
}

func closingFenceLine(line string, ch byte, minLen int) bool {
	i := skipFenceIndent(line)
	if i >= len(line) || line[i] != ch {
		return false
	}
	j := i
	for j < len(line) && line[j] == ch {
		j++
	}
	if j-i < minLen {
		return false
	}
	return strings.TrimSpace(line[j:]) == ""
}

func skipFenceIndent(line string) int {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	return i
}

func inlineCodeRanges(md string, protected []mdRange) []mdRange {
	var ranges []mdRange
	for i := 0; i < len(md); {
		if inRanges(i, protected) {
			i = rangeEndAt(i, protected)
			continue
		}
		if md[i] != '`' || isEscapedAt(md, i) {
			i++
			continue
		}
		run := 1
		for i+run < len(md) && md[i+run] == '`' {
			run++
		}
		closeAt := findClosingBackticks(md, i+run, run, protected)
		if closeAt < 0 {
			// Unmatched backticks are literal in CommonMark, but protecting the
			// rest of the line keeps repairs conservative around likely code.
			if end := strings.IndexByte(md[i:], '\n'); end >= 0 {
				ranges = append(ranges, mdRange{start: i, end: i + end})
			} else {
				ranges = append(ranges, mdRange{start: i, end: len(md)})
			}
			break
		}
		ranges = append(ranges, mdRange{start: i, end: closeAt + run})
		i = closeAt + run
	}
	return ranges
}

func findClosingBackticks(md string, from, run int, protected []mdRange) int {
	for i := from; i < len(md); {
		if inRanges(i, protected) {
			i = rangeEndAt(i, protected)
			continue
		}
		if md[i] != '`' || isEscapedAt(md, i) {
			i++
			continue
		}
		n := 1
		for i+n < len(md) && md[i+n] == '`' {
			n++
		}
		if n == run {
			return i
		}
		i += n
	}
	return -1
}

func linkDestinationRanges(md string, protected []mdRange) []mdRange {
	var ranges []mdRange
	for i := 0; i+1 < len(md); i++ {
		if inRanges(i, protected) {
			i = rangeEndAt(i, protected) - 1
			continue
		}
		if md[i] != ']' || md[i+1] != '(' || isEscapedAt(md, i) {
			continue
		}
		open := i + 1
		depth := 0
		for j := open; j < len(md); j++ {
			if inRanges(j, protected) {
				j = rangeEndAt(j, protected) - 1
				continue
			}
			if isEscapedAt(md, j) {
				continue
			}
			switch md[j] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					ranges = append(ranges, mdRange{start: open, end: j + 1})
					i = j
					j = len(md)
				}
			}
			if depth == 0 {
				break
			}
		}
	}
	return ranges
}

func findStrongEmphasisRuns(md string, protected []mdRange) []mdRange {
	var runs []mdRange
	for i := 0; i < len(md); {
		if inRanges(i, protected) {
			i = rangeEndAt(i, protected)
			continue
		}
		if md[i] != '*' || isEscapedAt(md, i) {
			i++
			continue
		}
		n := 1
		for i+n < len(md) && md[i+n] == '*' {
			n++
		}
		// Only plain "**" runs. Single, triple, and longer runs are left alone.
		if n == 2 {
			runs = append(runs, mdRange{start: i, end: i + n})
		}
		i += n
	}
	return runs
}

func isEscapedAt(s string, pos int) bool {
	backslashes := 0
	for i := pos - 1; i >= 0 && s[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func inRanges(pos int, ranges []mdRange) bool {
	for _, r := range ranges {
		if pos >= r.start && pos < r.end {
			return true
		}
	}
	return false
}

func rangeEndAt(pos int, ranges []mdRange) int {
	for _, r := range ranges {
		if pos >= r.start && pos < r.end {
			return r.end
		}
	}
	return pos + 1
}

func hasBlankLine(s string) bool {
	normalized := strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Contains(normalized, "\n\n")
}

func runeAt(s string, pos int) (rune, bool) {
	if pos < 0 || pos >= len(s) {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(s[pos:])
	return r, true
}

func runeBefore(s string, pos int) (rune, bool) {
	if pos <= 0 || pos > len(s) {
		return 0, false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:pos])
	return r, true
}

// referenceDefinitionRanges protects CommonMark link reference definitions
// such as `[x]: https://example.com/**a。**b`. Their destinations can contain
// delimiter-like text and must never be rewritten.
func referenceDefinitionRanges(md string, protected []mdRange) []mdRange {
	var ranges []mdRange
	for offset := 0; offset < len(md); {
		line, next := markdownLineAt(md, offset)
		if inRanges(offset, protected) {
			offset = next
			continue
		}
		indent := 0
		for indent < len(line) && indent < 4 && line[indent] == ' ' {
			indent++
		}
		rest := line[indent:]
		if indent <= 3 && strings.HasPrefix(rest, "[") {
			if close := strings.Index(rest, "]:"); close > 1 {
				ranges = append(ranges, mdRange{start: offset, end: next})
			}
		}
		offset = next
	}
	return ranges
}

// angleAutolinkRanges protects `<https://...>` / `<mailto:...>` autolinks.
func angleAutolinkRanges(md string, protected []mdRange) []mdRange {
	var ranges []mdRange
	for i := 0; i < len(md); {
		if inRanges(i, protected) {
			i = rangeEndAt(i, protected)
			continue
		}
		if md[i] != '<' || isEscapedAt(md, i) {
			i++
			continue
		}
		rel := strings.IndexByte(md[i+1:], '>')
		if rel < 0 {
			i++
			continue
		}
		end := i + 1 + rel + 1
		inside := md[i+1 : end-1]
		if looksLikeAutolink(inside) {
			ranges = append(ranges, mdRange{start: i, end: end})
			i = end
			continue
		}
		i++
	}
	return ranges
}

func looksLikeAutolink(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	colon := strings.IndexByte(s, ':')
	if colon >= 2 && colon <= 32 {
		valid := true
		for i := 0; i < colon; i++ {
			c := s[i]
			if i == 0 {
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
					valid = false
					break
				}
			} else if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '+' || c == '.' || c == '-') {
				valid = false
				break
			}
		}
		if valid {
			return true
		}
	}
	return colon < 0 && strings.Contains(s, "@")
}

// transformOutsideFencedCode applies fn only outside complete and unfinished
// backtick/tilde fenced code blocks. Platform-specific transforms must not
// inspect or rewrite diagram/code source.
func transformOutsideFencedCode(md string, fn func(string) string) string {
	ranges := fencedCodeRanges(md)
	if len(ranges) == 0 {
		return fn(md)
	}
	var b strings.Builder
	b.Grow(len(md))
	last := 0
	for _, r := range ranges {
		if r.start < last || r.end > len(md) {
			continue
		}
		b.WriteString(fn(md[last:r.start]))
		b.WriteString(md[r.start:r.end])
		last = r.end
	}
	b.WriteString(fn(md[last:]))
	return b.String()
}
