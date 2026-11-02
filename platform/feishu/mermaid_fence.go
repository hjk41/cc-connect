package feishu

import "strings"

// mermaidFence is a complete CommonMark-style fenced code block whose info
// label is "mermaid" (case-insensitive). Offsets cover the entire fence,
// including its opening and closing marker lines.
type mermaidFence struct {
	start int
	end   int
	// source is the diagram source between the marker lines, with trailing
	// line endings removed. The original bytes are preserved by offsets for
	// fallback rendering.
	source string
}

// findMermaidFences returns complete mermaid fences in document order. It
// tracks all fenced blocks so a mermaid-looking line inside another code block
// is never treated as a diagram. Unclosed fences are ignored.
func findMermaidFences(md string) []mermaidFence {
	var fences []mermaidFence
	var (
		inFence      bool
		fenceChar    byte
		fenceLen     int
		fenceStart   int
		contentStart int
		mermaid      bool
	)

	for offset := 0; offset < len(md); {
		line, next := markdownLineAt(md, offset)
		if !inFence {
			if ch, n, infoOK := openingFenceLine(line); infoOK {
				inFence = true
				fenceChar = ch
				fenceLen = n
				fenceStart = offset
				mermaid = isMermaidInfoLine(line)
				contentStart = next
			}
		} else if closingFenceLine(line, fenceChar, fenceLen) {
			if mermaid {
				fences = append(fences, mermaidFence{
					start:  fenceStart,
					end:    next,
					source: strings.TrimRight(md[contentStart:offset], "\r\n"),
				})
			}
			inFence = false
		}
		offset = next
	}
	return fences
}

func isMermaidInfoLine(line string) bool {
	i := skipFenceIndent(line)
	if i >= len(line) {
		return false
	}
	ch := line[i]
	j := i
	for j < len(line) && line[j] == ch {
		j++
	}
	if j-i < 3 {
		return false
	}
	info := strings.TrimSpace(line[j:])
	if info == "" {
		return false
	}
	label := info
	if fields := strings.Fields(info); len(fields) > 0 {
		label = fields[0]
	}
	return strings.EqualFold(label, "mermaid")
}
