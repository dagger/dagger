// Package ripgrep collects ripgrep's output within budgets, so that a search
// matching everything (an agent searching for the regexp ".", say) cannot
// exhaust the memory of whoever collects it: the engine, for Directory.search
// and File.search, or the CLI, for a search of a host workspace.
//
// Without budgets every matched line became a result, and every submatch -
// one per byte for "." - an object of its own: 1.28 MB of text searched for
// "." made the engine allocate ~3.5 GB and the querying CLI grow to ~2.2 GB,
// and a single 1 MB minified line cost the engine 1.1 GB.
package ripgrep

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Limits bounds what a search collects. A zero field means no bound.
type Limits struct {
	// MaxResults is how many results a search may collect when the caller set
	// no limit of its own: one more fails the search with a *BudgetError. An
	// explicit limit replaces it, and truncates instead.
	MaxResults int
	// MaxBytes bounds the estimated size of the collected results (paths,
	// matched lines and submatches, with a fixed overhead for each result and
	// submatch, roughly their JSON size), limit or not: going over fails the
	// search with a *BudgetError.
	MaxBytes int
	// MaxLineBytes cuts a result's matched lines longer than this, at a UTF-8
	// boundary, ending them with a marker saying how much was omitted.
	MaxLineBytes int
	// MaxSubmatches is how many submatches a result keeps: the first ones
	// that lie within its (possibly cut) matched lines.
	MaxSubmatches int
}

// Default budgets: far above what a useful search returns (no reader, human
// or model, reads ten thousand lines), and far below what makes an engine
// run out of memory.
const (
	DefaultMaxResults    = 10000
	DefaultMaxBytes      = 8 << 20
	DefaultMaxLineBytes  = 8 << 10
	DefaultMaxSubmatches = 64
)

// DefaultLimits are the budgets a search runs within unless its caller says
// otherwise.
func DefaultLimits() Limits {
	return Limits{
		MaxResults:    DefaultMaxResults,
		MaxBytes:      DefaultMaxBytes,
		MaxLineBytes:  DefaultMaxLineBytes,
		MaxSubmatches: DefaultMaxSubmatches,
	}
}

// Estimated overheads, in bytes, of a result and of a submatch beyond their
// text: about what each costs in JSON.
const (
	resultOverhead   = 64
	submatchOverhead = 32
)

// Result is one match: a line, or several lines in multiline mode.
type Result struct {
	Path           string
	LineNumber     int
	AbsoluteOffset int
	Lines          string
	Submatches     []Submatch
}

// Submatch is one match within a Result's lines.
type Submatch struct {
	Text  string
	Start int
	End   int
}

// BudgetError is the error of a search that matched more than its budget
// allows.
type BudgetError struct {
	// Unit names what was counted: "lines" or "files".
	Unit string
	// MaxResults is set when the search matched more than this many results
	// with no limit set.
	MaxResults int
	// MaxBytes is set when the results grew larger than this.
	MaxBytes int
	// Limited reports that the search set a limit of its own.
	Limited bool
}

func (e *BudgetError) Error() string {
	if e.MaxResults > 0 {
		return fmt.Sprintf("search matched more than %d %s: narrow the pattern, search fewer paths (paths, globs), or set a limit", e.MaxResults, e.Unit)
	}
	limit := "set a limit"
	if e.Limited {
		limit = "set a lower limit"
	}
	return fmt.Sprintf("search results exceed %d MiB: narrow the pattern, search fewer paths (paths, globs), or %s", e.MaxBytes>>20, limit)
}

// IsBudgetError reports whether err is, or wraps, a *BudgetError.
func IsBudgetError(err error) bool {
	var be *BudgetError
	return errors.As(err, &be)
}

// Budget accounts for the results a search collects.
type Budget struct {
	limits Limits
	limit  *int
	unit   string
	n      int
	bytes  int
}

// NewBudget returns the budget of a search with the given limits and the
// caller's own limit (nil for none), counting unit ("lines" or "files").
func NewBudget(limits Limits, limit *int, unit string) *Budget {
	return &Budget{limits: limits, limit: limit, unit: unit}
}

// Add accounts for one more result of about cost bytes. It fails, without
// adding it, when the result does not fit the budget. full reports that the
// caller's limit is reached with it: the search should stop after keeping it.
func (b *Budget) Add(cost int) (full bool, err error) {
	if b.limit == nil && b.limits.MaxResults > 0 && b.n >= b.limits.MaxResults {
		return false, &BudgetError{Unit: b.unit, MaxResults: b.limits.MaxResults}
	}
	if b.limits.MaxBytes > 0 && b.bytes+cost > b.limits.MaxBytes {
		return false, &BudgetError{Unit: b.unit, MaxBytes: b.limits.MaxBytes, Limited: b.limit != nil}
	}
	b.n++
	b.bytes += cost
	return b.limit != nil && b.n >= *b.limit, nil
}

// TruncateLines cuts lines to at most maxBytes (0: no cut) at a UTF-8
// boundary, ending them with a marker saying how many bytes were omitted
// (and the newline, if they ended with one). It returns the new lines and
// how many bytes of the original they keep.
func TruncateLines(lines string, maxBytes int) (string, int) {
	body, hasNL := strings.CutSuffix(lines, "\n")
	if maxBytes <= 0 || len(body) <= maxBytes {
		return lines, len(lines)
	}
	cut := runeCut(body, maxBytes)
	nl := ""
	if hasNL {
		nl = "\n"
	}
	// The concatenation copies: the result doesn't pin the long original.
	return body[:cut] + omittedMarker(len(body)-cut) + nl, cut
}

// TruncateReadLine is TruncateLines for a line (without its newline) that
// ScanLines already cut, dropping `dropped` bytes after it: the marker counts
// those too.
func TruncateReadLine(line string, maxBytes, dropped int) string {
	if dropped == 0 {
		s, _ := TruncateLines(line, maxBytes)
		return s
	}
	var cut int
	if maxBytes > 0 && len(line) > maxBytes {
		cut = runeCut(line, maxBytes)
	} else {
		// The reader's cut may have split the last character.
		cut = len(line)
		for i := 1; i < utf8.UTFMax && i <= cut; i++ {
			if utf8.RuneStart(line[cut-i]) {
				if !utf8.FullRuneInString(line[cut-i:]) {
					cut -= i
				}
				break
			}
		}
	}
	return line[:cut] + omittedMarker(len(line)-cut+dropped)
}

// Options configure a parse.
type Options struct {
	Limits Limits
	// Limit is the caller's own limit on results; nil for none.
	Limit *int
	// Keep, when set, selects the results to collect by path: the others are
	// dropped as they are read, before they count against the limit or the
	// budgets.
	Keep func(path string) bool
	// OnSkip, when set, is told about each match skipped because its path or
	// lines are not UTF-8.
	OnSkip func(reason string)
}

// ParseFiles reads `rg --files-with-matches` output, one path per line,
// calling fn with each path. It reports whether it stopped early on reaching
// the limit: the caller should then stop ripgrep, whose output was not read
// to the end.
func ParseFiles(r io.Reader, opts Options, fn func(path string) error) (limitReached bool, err error) {
	budget := NewBudget(opts.Limits, opts.Limit, "files")
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scan.Scan() {
		line := scan.Text()
		if line == "" || (opts.Keep != nil && !opts.Keep(line)) {
			continue
		}
		full, err := budget.Add(len(line) + resultOverhead)
		if err != nil {
			return false, err
		}
		if err := fn(line); err != nil {
			return false, err
		}
		if full {
			return true, nil
		}
	}
	return false, scan.Err()
}

// ParseJSON reads `rg --json` output, calling fn with each match, cut to the
// limits. It reports whether it stopped early on reaching the limit: the
// caller should then stop ripgrep, whose output was not read to the end.
//
// The output is decoded token by token, never a whole message at once: one
// long line searched for "." is a single message as long as ~40 times the
// line (a submatch per byte), and decoding it whole made the engine hold all
// of it and every submatch in it.
func ParseJSON(r io.Reader, opts Options, fn func(Result) error) (limitReached bool, err error) {
	budget := NewBudget(opts.Limits, opts.Limit, "lines")
	dec := json.NewDecoder(r)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if tok != json.Delim('{') {
			return false, fmt.Errorf("ripgrep output: expected an object, got %v", tok)
		}
		msgType := ""
		var res *Result
		for dec.More() {
			key, err := objectKey(dec)
			if err != nil {
				return false, err
			}
			switch {
			case key == "type":
				if err := dec.Decode(&msgType); err != nil {
					return false, fmt.Errorf("ripgrep output: type: %w", err)
				}
			case key == "data" && (msgType == "match" || msgType == ""):
				// ripgrep writes the type first; should it ever not, the data
				// is decoded as a match's and dropped below if it isn't one.
				res, err = decodeMatch(dec, opts)
				if err != nil {
					return false, err
				}
			default:
				if err := skipValue(dec); err != nil {
					return false, err
				}
			}
		}
		if _, err := dec.Token(); err != nil { // the closing }
			return false, err
		}
		if msgType != "match" || res == nil || (opts.Keep != nil && !opts.Keep(res.Path)) {
			continue
		}
		cost := resultOverhead + len(res.Path) + len(res.Lines)
		for _, sm := range res.Submatches {
			cost += submatchOverhead + len(sm.Text)
		}
		full, err := budget.Add(cost)
		if err != nil {
			return false, err
		}
		if err := fn(*res); err != nil {
			return false, err
		}
		if full {
			return true, nil
		}
	}
}

type rgContent struct {
	Text  string `json:"text,omitempty"`
	Bytes []byte `json:"bytes,omitempty"`
}

type rgSubmatch struct {
	Match rgContent `json:"match"`
	Start int       `json:"start"`
	End   int       `json:"end"`
}

// decodeMatch decodes a match message's data. It returns nil for a match
// that is skipped (not UTF-8).
func decodeMatch(dec *json.Decoder, opts Options) (*Result, error) {
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	var (
		res        Result
		path       rgContent
		lines      rgContent
		lineNumber *int
		submatches []Submatch
	)
	for dec.More() {
		key, err := objectKey(dec)
		if err != nil {
			return nil, err
		}
		switch key {
		case "path":
			err = dec.Decode(&path)
		case "lines":
			err = dec.Decode(&lines)
		case "line_number":
			err = dec.Decode(&lineNumber)
		case "absolute_offset":
			err = dec.Decode(&res.AbsoluteOffset)
		case "submatches":
			submatches, err = decodeSubmatches(dec, opts.Limits)
		default:
			err = skipValue(dec)
		}
		if err != nil {
			return nil, fmt.Errorf("ripgrep output: match %s: %w", key, err)
		}
	}
	if _, err := dec.Token(); err != nil { // the closing }
		return nil, err
	}
	if len(path.Bytes) > 0 {
		if opts.OnSkip != nil {
			opts.OnSkip("non-UTF-8 path")
		}
		return nil, nil
	}
	if len(lines.Bytes) > 0 {
		if opts.OnSkip != nil {
			opts.OnSkip("non-UTF-8 content in " + path.Text)
		}
		return nil, nil
	}
	res.Path = path.Text
	if lineNumber != nil {
		res.LineNumber = *lineNumber
	}
	var kept int
	res.Lines, kept = TruncateLines(lines.Text, opts.Limits.MaxLineBytes)
	for _, sm := range submatches {
		if sm.End <= kept {
			res.Submatches = append(res.Submatches, sm)
		}
	}
	return &res, nil
}

// decodeSubmatches decodes a match's submatches one at a time, keeping the
// first ones within the limits and dropping the rest as they go by.
func decodeSubmatches(dec *json.Decoder, limits Limits) ([]Submatch, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok == nil { // null
		return nil, nil
	}
	if tok != json.Delim('[') {
		return nil, fmt.Errorf("expected an array, got %v", tok)
	}
	var kept []Submatch
	for dec.More() {
		var sm rgSubmatch
		if err := dec.Decode(&sm); err != nil {
			return nil, err
		}
		if limits.MaxSubmatches > 0 && len(kept) >= limits.MaxSubmatches {
			continue
		}
		if limits.MaxLineBytes > 0 && sm.End > limits.MaxLineBytes {
			continue
		}
		if len(sm.Match.Bytes) > 0 {
			continue
		}
		kept = append(kept, Submatch{Text: sm.Match.Text, Start: sm.Start, End: sm.End})
	}
	if _, err := dec.Token(); err != nil { // the closing ]
		return nil, err
	}
	return kept, nil
}

func objectKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	key, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("ripgrep output: expected an object key, got %v", tok)
	}
	return key, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != want {
		return fmt.Errorf("ripgrep output: expected %v, got %v", want, tok)
	}
	return nil
}

// skipValue reads past the next value, token by token, so that skipping a
// large one never holds it whole.
func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

// ScanLines calls fn with each line of r, without its newline: its first
// maxBytes (0: all of it), and how many more bytes it had (see
// TruncateReadLine). Unlike a bufio.Scanner, a line of any length is read,
// and only its first maxBytes are kept.
func ScanLines(r io.Reader, maxBytes int, fn func(line string, dropped int) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	dropped := 0
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(chunk) > 0 {
			if room := maxBytes - len(buf); maxBytes <= 0 || room >= len(chunk) {
				buf = append(buf, chunk...)
			} else {
				if room > 0 {
					buf = append(buf, chunk[:room]...)
				}
				dropped += len(chunk) - max(room, 0)
			}
		}
		if err == io.EOF {
			if len(buf) > 0 || dropped > 0 {
				return fn(string(buf), dropped)
			}
			return nil
		}
		if err != nil {
			return err
		}
		if isPrefix {
			continue
		}
		if err := fn(string(buf), dropped); err != nil {
			return err
		}
		buf = buf[:0]
		dropped = 0
	}
}

// runeCut is the largest length up to maxBytes at which s can be cut
// without splitting a UTF-8 sequence.
func runeCut(s string, maxBytes int) int {
	if len(s) <= maxBytes {
		return len(s)
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

func omittedMarker(n int) string {
	return fmt.Sprintf("[... omitted %d bytes of long line]", n)
}
