package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/vektah/gqlparser/v2/ast"
)

type SearchResult struct {
	FilePath       string            `field:"true" doc:"The path to the file that matched."`
	LineNumber     int               `field:"true" doc:"The first line that matched."`
	AbsoluteOffset int               `field:"true" doc:"The byte offset of this line within the file."`
	MatchedLines   string            `field:"true" doc:"The line content that matched."`
	Submatches     []*SearchSubmatch `field:"true" doc:"Sub-match positions and content within the matched lines."`
}

func (*SearchResult) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SearchResult",
		NonNull:   true,
	}
}

type SearchSubmatch struct {
	Text  string `field:"true" doc:"The matched text."`
	Start int    `field:"true" doc:"The match's start offset within the matched lines."`
	End   int    `field:"true" doc:"The match's end offset within the matched lines."`
}

func (*SearchSubmatch) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SearchSubmatch",
		NonNull:   true,
	}
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

type rgJSON struct {
	Type string `json:"type"`
	Data struct {
		Path           rgContent `json:"path"`
		Lines          rgContent `json:"lines"`
		LineNumber     int       `json:"line_number"`
		AbsoluteOffset int       `json:"absolute_offset"`
		Submatches     []struct {
			Match rgContent `json:"match"`
			Start int       `json:"start"`
			End   int       `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

type rgContent struct {
	Text  string `json:"text,omitempty"`
	Bytes []byte `json:"bytes,omitempty"`
}

func (opts *SearchOpts) RunRipgrep(ctx context.Context, rg *exec.Cmd, verbose bool) ([]*SearchResult, error) {
	var errBuf bytes.Buffer
	rg.Stderr = &errBuf

	out, err := rg.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer out.Close()
	if err := rg.Start(); err != nil {
		return nil, err
	}
	var errs error
	results, err := opts.parseRgOutput(ctx, out, verbose)
	if err != nil {
		// NOTE: probably overkill, but trying to avoid ever seeing a useless error
		// like "broken pipe" instead of "exit status 128"
		errs = errors.Join(errs, err)
	}
	if err := rg.Wait(); err != nil {
		if rg.ProcessState != nil && rg.ProcessState.ExitCode() == 1 {
			return []*SearchResult{}, nil
		}
		if errBuf.Len() > 0 {
			errs = errors.Join(errs, fmt.Errorf("ripgrep error: %s", errBuf.String()))
		} else {
			errs = errors.Join(errs, err)
		}
	}
	return results, errs
}

func (opts *SearchOpts) parseRgOutput(ctx context.Context, rgOut io.ReadCloser, verbose bool) ([]*SearchResult, error) {
	defer rgOut.Close()

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	results := []*SearchResult{}

	// Only requested files; parse returned paths
	if opts.FilesOnly {
		scan := bufio.NewScanner(rgOut)
		for scan.Scan() {
			line := scan.Text()
			if line == "" {
				continue
			}
			if verbose {
				fmt.Fprintln(stdio.Stdout, line)
			}
			results = append(results, &SearchResult{FilePath: line})
		}
		if err := scan.Err(); err != nil {
			return results, err
		}
		return results, nil
	}

	dec := json.NewDecoder(rgOut)
	for {
		var match rgJSON
		if err := dec.Decode(&match); err != nil {
			if err == io.EOF {
				break
			}
			return results, err
		}
		if match.Type != "match" {
			continue
		}
		data := match.Data
		if len(match.Data.Path.Bytes) > 0 {
			slog.Warn("skipping non-utf8 content", "content", base64.StdEncoding.EncodeToString(data.Path.Bytes))
			continue
		}
		if len(data.Lines.Bytes) > 0 {
			slog.Warn("skipping non-utf8 path", "content", base64.StdEncoding.EncodeToString(data.Lines.Bytes))
			continue
		}

		result := &SearchResult{
			FilePath:       data.Path.Text,
			LineNumber:     data.LineNumber,
			AbsoluteOffset: data.AbsoluteOffset,
			MatchedLines:   data.Lines.Text,
		}

		for _, match := range data.Submatches {
			result.Submatches = append(result.Submatches, &SearchSubmatch{
				Text:  match.Match.Text,
				Start: match.Start,
				End:   match.End,
			})
		}

		ensureLn := result.MatchedLines
		if !strings.HasSuffix(ensureLn, "\n") {
			ensureLn += "\n"
		}
		if verbose {
			fmt.Fprintf(stdio.Stdout, "%s:%d:%s", result.FilePath, result.LineNumber, ensureLn)
		}

		results = append(results, result)
		if opts.Limit != nil && len(results) >= *opts.Limit {
			break
		}
	}
	return results, nil
}
