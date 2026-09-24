package core

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type logPageOpts struct {
	scope, grep                      string
	offset, limit, fromLine, context int
}

// Line addresses are one-based in a fixed span/scope capture, before grep.
// Historical captures are immutable. Live captures may grow or complete a
// trailing fragment, so callers should not treat them as snapshot cursors.
func renderLogPage(span string, lines []capturedLine, opt logPageOpts) (string, error) {
	if opt.fromLine < 0 || opt.context < 0 || opt.context > 100 {
		return "", fmt.Errorf("fromLine must be non-negative; context must be between 0 and 100")
	}
	if opt.fromLine > 0 && opt.offset != 0 {
		return "", fmt.Errorf("fromLine and offset are mutually exclusive")
	}
	if opt.limit <= 0 {
		opt.limit = 100
	}
	opt.limit = min(opt.limit, 500)
	end := len(lines) - max(opt.offset, 0)
	if end <= 0 {
		return "", fmt.Errorf("offset %d skips all %d available lines; retry with offset 0", opt.offset, len(lines))
	}
	start := max(opt.fromLine-1, 0)
	if start >= end {
		return "", fmt.Errorf("fromLine %d exceeds %d available lines", opt.fromLine, end)
	}
	var re *regexp.Regexp
	if opt.grep != "" {
		var err error
		re, err = regexp.Compile(opt.grep)
		if err != nil {
			return "", fmt.Errorf("invalid grep pattern %q: %w", opt.grep, err)
		}
	}
	// Merge overlapping context windows; every line keeps its original address.
	selected := make([]bool, end)
	matches := 0
	for i := 0; i < end; i++ {
		if re == nil || re.MatchString(lines[i].text) {
			if i >= start {
				matches++
			}
			lo, hi := i, i+1
			if re != nil {
				lo, hi = max(start, i-opt.context), min(end, i+opt.context+1)
			}
			for j := lo; j < hi; j++ {
				selected[j] = true
			}
		}
	}
	var indices []int
	for i := start; i < end; i++ {
		if selected[i] {
			indices = append(indices, i)
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Logs span=%s scope=%s; %d lines available; addresses precede grep, record order (live captures may grow).\n", span, opt.scope, len(lines))
	if opt.scope == "causal" {
		out.WriteString("Causal scope includes descendants and cause-linked work, not just own logs; membership does not establish error causality.\n")
	}
	if re != nil {
		fmt.Fprintf(&out, "%d grep matches; context=%d.\n", matches, opt.context)
	}
	if len(indices) == 0 {
		out.WriteString("(no matching lines)\n")
		return out.String(), nil
	}
	if len(indices) > opt.limit {
		if opt.fromLine > 0 {
			indices = indices[:opt.limit]
		} else {
			indices = indices[len(indices)-opt.limit:]
		}
	}
	first, last := indices[0], indices[0]-1
	for _, i := range indices {
		line := lines[i]
		stamp := "unknown"
		if line.timestamp != 0 {
			stamp = time.Unix(0, line.timestamp).UTC().Format(time.RFC3339Nano)
		}
		text := fmt.Sprintf("%6d→[span=%s stream=%d time=%s] %s\n", i+1, line.producer.spanID, line.producer.stream, stamp, clampLineBytes(line.text, llmLogsMaxLineLen))
		if out.Len()+len(text) > 24*1024 {
			break
		}
		if i > last+1 {
			out.WriteString("...\n")
		}
		out.WriteString(text)
		last = i
	}
	// Exact calls, rather than backwards arithmetic, make either direction usable.
	if first > 0 {
		fmt.Fprintf(&out, "Earlier: ReadLogs(span: %q, scope: %q, offset: %d, limit: %d, grep: %q, context: %d)\n", span, opt.scope, len(lines)-first, opt.limit, opt.grep, opt.context)
	}
	if last+1 < end {
		fmt.Fprintf(&out, "Next: ReadLogs(span: %q, scope: %q, fromLine: %d, limit: %d, grep: %q, context: %d)\n", span, opt.scope, last+2, opt.limit, opt.grep, opt.context)
	}
	fmt.Fprintf(&out, "Range: ReadLogs(span: %q, scope: %q, fromLine: %d, limit: %d)\n", span, opt.scope, first+1, last-first+1)
	return out.String(), nil
}
