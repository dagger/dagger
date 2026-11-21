package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/ripgrep"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
	"github.com/vektah/gqlparser/v2/ast"
)

type SearchResult struct {
	FilePath       string            `field:"true" doc:"The path to the file that matched."`
	LineNumber     int               `field:"true" doc:"The first line that matched."`
	AbsoluteOffset int               `field:"true" doc:"The byte offset of this line within the file."`
	MatchedLines   string            `field:"true" doc:"The line content that matched."`
	Submatches     []*SearchSubmatch `field:"true" doc:"Sub-match positions and content within the matched lines."`
}

var _ dagql.PersistedObject = (*SearchResult)(nil)
var _ dagql.PersistedObjectDecoder = (*SearchResult)(nil)

func (*SearchResult) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SearchResult",
		NonNull:   true,
	}
}

type persistedSearchResult struct {
	FilePath       string                     `json:"filePath"`
	LineNumber     int                        `json:"lineNumber"`
	AbsoluteOffset int                        `json:"absoluteOffset"`
	MatchedLines   string                     `json:"matchedLines"`
	Submatches     []*persistedSearchSubmatch `json:"submatches,omitempty"`
}

func (r *SearchResult) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if r == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted search result: nil search result")
	}
	payload := persistedSearchResult{
		FilePath:       r.FilePath,
		LineNumber:     r.LineNumber,
		AbsoluteOffset: r.AbsoluteOffset,
		MatchedLines:   r.MatchedLines,
		Submatches:     make([]*persistedSearchSubmatch, 0, len(r.Submatches)),
	}
	for _, submatch := range r.Submatches {
		if submatch == nil {
			continue
		}
		payload.Submatches = append(payload.Submatches, &persistedSearchSubmatch{
			Text:  submatch.Text,
			Start: submatch.Start,
			End:   submatch.End,
		})
	}
	return encodePersistedObjectPayload(payload)
}

func (*SearchResult) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedSearchResult
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted search result payload: %w", err)
	}
	result := &SearchResult{
		FilePath:       persisted.FilePath,
		LineNumber:     persisted.LineNumber,
		AbsoluteOffset: persisted.AbsoluteOffset,
		MatchedLines:   persisted.MatchedLines,
		Submatches:     make([]*SearchSubmatch, 0, len(persisted.Submatches)),
	}
	for _, submatch := range persisted.Submatches {
		if submatch == nil {
			continue
		}
		result.Submatches = append(result.Submatches, &SearchSubmatch{
			Text:  submatch.Text,
			Start: submatch.Start,
			End:   submatch.End,
		})
	}
	return result, nil
}

type SearchSubmatch struct {
	Text  string `field:"true" doc:"The matched text."`
	Start int    `field:"true" doc:"The match's start offset within the matched lines."`
	End   int    `field:"true" doc:"The match's end offset within the matched lines."`
}

var _ dagql.PersistedObject = (*SearchSubmatch)(nil)
var _ dagql.PersistedObjectDecoder = (*SearchSubmatch)(nil)

func (*SearchSubmatch) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SearchSubmatch",
		NonNull:   true,
	}
}

type persistedSearchSubmatch struct {
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func (m *SearchSubmatch) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if m == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted search submatch: nil search submatch")
	}
	return encodePersistedObjectPayload(persistedSearchSubmatch{
		Text:  m.Text,
		Start: m.Start,
		End:   m.End,
	})
}

func (*SearchSubmatch) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedSearchSubmatch
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted search submatch payload: %w", err)
	}
	return &SearchSubmatch{
		Text:  persisted.Text,
		Start: persisted.Start,
		End:   persisted.End,
	}, nil
}

type SearchOpts struct {
	Pattern     string
	Literal     bool `default:"false"`
	Multiline   bool `default:"false"`
	Dotall      bool `default:"false"`
	Insensitive bool `default:"false"`
	SkipIgnored bool `default:"false"`
	SkipHidden  bool `default:"false"`
	FilesOnly   bool `default:"false"`
	Limit       *int

	// Budget, when set, replaces the budgets the search runs within
	// (ripgrep.DefaultLimits), for callers inside the engine. It is not an
	// argument.
	Budget *ripgrep.Limits `name:"-"`
}

// limits are the budgets the search runs within.
func (opts SearchOpts) limits() ripgrep.Limits {
	if opts.Budget != nil {
		return *opts.Budget
	}
	return ripgrep.DefaultLimits()
}

func (opts SearchOpts) Args() []dagql.Argument {
	return []dagql.Argument{
		dagql.Arg("pattern").Doc(`The text to match.`),
		dagql.Arg("literal").Doc(`Interpret the pattern as a literal string instead of a regular expression.`),
		dagql.Arg("multiline").Doc(`Enable searching across multiple lines.`),
		dagql.Arg("dotall").Doc(`Allow the . pattern to match newlines in multiline mode.`),
		dagql.Arg("insensitive").Doc(`Enable case-insensitive matching.`),
		dagql.Arg("skipIgnored").Doc(`Honor .gitignore, .ignore, and .rgignore files.`),
		dagql.Arg("skipHidden").Doc(`Skip hidden files (files starting with .).`),
		dagql.Arg("filesOnly").Doc(`Only return matching files, not lines and content`),
		dagql.Arg("limit").Doc(`Limit the number of results to return`),
	}
}

func (opts SearchOpts) RipgrepArgs() []string {
	var args []string
	if opts.Literal {
		args = append(args, "--fixed-strings")
	}
	if opts.Multiline {
		args = append(args, "--multiline")
	}
	if opts.Dotall {
		args = append(args, "--multiline-dotall")
	}
	if opts.Insensitive {
		args = append(args, "--ignore-case")
	}
	if !opts.SkipIgnored {
		args = append(args, "--no-ignore")
	}
	if !opts.SkipHidden {
		args = append(args, "--hidden")
	}
	if opts.FilesOnly {
		args = append(args, "--files-with-matches")
	} else {
		args = append(args, "--json")
	}
	// NOTE: opts.Limit is handled while parsing results; there isn't a flag to
	// limit total results, only to limit results per file
	args = append(args, "--regexp="+opts.Pattern)
	// Explicitly forbid following symlinks (even though it's the default)
	args = append(args, "--no-follow")
	return args
}

// RunRipgrep runs rg, built from RipgrepArgs, and collects its results within
// the search's limit and budgets (see engine/ripgrep). Once either trips, rg
// is killed rather than drained: whatever it would still print is unwanted.
// With verbose, the results are also written to the span's stdio (see
// EmitSearchResults).
func (opts *SearchOpts) RunRipgrep(ctx context.Context, rg *exec.Cmd, verbose bool) ([]*SearchResult, error) {
	var errBuf bytes.Buffer
	rg.Stderr = &errBuf

	out, err := rg.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer out.Close()
	finish := enginetel.PrepareCommandNetwork(ctx, rg)
	defer finish()
	if err := rg.Start(); err != nil {
		return nil, err
	}
	results, limitReached, parseErr := opts.parseRgOutput(ctx, out)
	if limitReached || parseErr != nil {
		// Stop rg now instead of letting it run on until it next writes to
		// the closed pipe: it may be walking a large tree.
		_ = rg.Process.Kill()
		out.Close()
		_ = rg.Wait()
		if parseErr != nil {
			if !ripgrep.IsBudgetError(parseErr) && errBuf.Len() > 0 {
				// Avoid a useless error like "broken pipe" or "unexpected EOF"
				// when rg said what went wrong.
				parseErr = errors.Join(parseErr, fmt.Errorf("ripgrep error: %s", errBuf.String()))
			}
			return nil, parseErr
		}
	} else if err := rg.Wait(); err != nil {
		if rg.ProcessState != nil && rg.ProcessState.ExitCode() == 1 {
			return []*SearchResult{}, nil
		}
		if errBuf.Len() > 0 {
			if engine.RipgrepNoFilesSearched(errBuf.String()) {
				return []*SearchResult{}, nil
			}
			return results, fmt.Errorf("ripgrep error: %s", errBuf.String())
		}
		return results, err
	}
	if verbose {
		EmitSearchResults(ctx, results, opts.FilesOnly)
	}
	return results, nil
}

// parseRgOutput collects rg's results. It reports whether it stopped early on
// reaching the search's limit.
func (opts *SearchOpts) parseRgOutput(ctx context.Context, rgOut io.Reader) ([]*SearchResult, bool, error) {
	logger := slog.SpanLogger(ctx, InstrumentationLibrary)
	parseOpts := ripgrep.Options{
		Limits: opts.limits(),
		Limit:  opts.Limit,
		OnSkip: func(reason string) {
			logger.Warn("skipping match", "reason", reason)
		},
	}

	results := []*SearchResult{}
	if opts.FilesOnly {
		limitReached, err := ripgrep.ParseFiles(rgOut, parseOpts, func(path string) error {
			results = append(results, &SearchResult{FilePath: path})
			return nil
		})
		return results, limitReached, err
	}
	limitReached, err := ripgrep.ParseJSON(rgOut, parseOpts, func(r ripgrep.Result) error {
		results = append(results, NewSearchResult(r))
		return nil
	})
	return results, limitReached, err
}

// NewSearchResult converts a parsed ripgrep result.
func NewSearchResult(r ripgrep.Result) *SearchResult {
	result := &SearchResult{
		FilePath:       r.Path,
		LineNumber:     r.LineNumber,
		AbsoluteOffset: r.AbsoluteOffset,
		MatchedLines:   r.Lines,
	}
	if len(r.Submatches) > 0 {
		result.Submatches = make([]*SearchSubmatch, len(r.Submatches))
		for i, sm := range r.Submatches {
			result.Submatches[i] = &SearchSubmatch{Text: sm.Text, Start: sm.Start, End: sm.End}
		}
	}
	return result
}

// What EmitSearchResults writes at most: a search's results are kept within
// their budgets already, but every log line is copied into the session's
// telemetry and streamed to its clients, which cost far more than the line
// (an LLM reading a tool call's logs sees only its last few lines anyway).
const (
	searchLogMaxLines = 1000
	searchLogMaxBytes = 256 << 10
)

// EmitSearchResults writes search results to the span's stdio, so they show
// up in progress output: the first searchLogMaxLines of them, up to
// searchLogMaxBytes, then a line counting the rest.
func EmitSearchResults(ctx context.Context, results []*SearchResult, filesOnly bool) {
	if len(results) == 0 {
		return
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()
	shown, written := 0, 0
	for _, result := range results {
		var line string
		if filesOnly {
			line = result.FilePath + "\n"
		} else {
			line = fmt.Sprintf("%s:%d:%s", result.FilePath, result.LineNumber, result.MatchedLines)
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
		}
		if shown >= searchLogMaxLines || written+len(line) > searchLogMaxBytes {
			break
		}
		io.WriteString(stdio.Stdout, line)
		shown++
		written += len(line)
	}
	if rest := len(results) - shown; rest > 0 {
		fmt.Fprintf(stdio.Stdout, "... %d more results not shown\n", rest)
	}
}
