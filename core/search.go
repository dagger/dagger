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
	FilePath     string `field:"true"`
	LineNumber   int    `field:"true"`
	MatchedLines string `field:"true"`
}

func (*SearchResult) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SearchResult",
		NonNull:   true,
	}
}

type SearchOpts struct {
	Pattern    string
	Literal    bool `default:"false"`
	Multiline  bool `default:"false"`
	Dotall     bool `default:"false"`
	IgnoreCase bool `default:"false"`
	FilesOnly  bool `default:"false"`
	Limit      *int
}

func (opts SearchOpts) Args() []dagql.Argument {
	return []dagql.Argument{
		dagql.Arg("pattern").Doc(`The text to match.`),
		dagql.Arg("literal").Doc(`Interpret the pattern as a literal string instead of a regular expression.`),
		dagql.Arg("multiline").Doc(`Enable searching across multiple lines.`),
		dagql.Arg("dotall").Doc(`Allow the . pattern to match newlines in multiline mode.`),
		dagql.Arg("ignoreCase").Doc(`Enable case-insensitive matching.`),
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
	if opts.IgnoreCase {
		args = append(args, "--ignore-case")
	}
	if opts.FilesOnly {
		args = append(args, "--files-with-matches")
	} else {
		args = append(args, "--json")
	}
	// NOTE: opts.Limit is handled while parsing results; there isn't a flag to
	// limit total results, only to limit results per file
	args = append(args, opts.Pattern)
	return args
}

type LLMDisplayer interface {
	LLMDisplay() string
}

var _ LLMDisplayer = (*SearchResult)(nil)

func (s *SearchResult) LLMDisplay() string {
	if s.LineNumber == 0 {
		return s.FilePath + "\n"
	}
	return fmt.Sprintf("%s:%d:%s", s.FilePath, s.LineNumber, s.MatchedLines)
}

type rgJSON struct {
	Type string `json:"type"`
	Data struct {
		Path       rgContent `json:"path"`
		Lines      rgContent `json:"lines"`
		LineNumber int       `json:"line_number"`
		// unused... for now?
		// AbsoluteOffset int       `json:"absolute_offset"`
		// Submatches     []struct {
		// 	Match rgContent `json:"match"`
		// 	Start int       `json:"start"`
		// 	End   int       `json:"end"`
		// } `json:"submatches"`
	} `json:"data"`
}

type rgContent struct {
	Text  string `json:"text,omitempty"`
	Bytes []byte `json:"bytes,omitempty"`
}

func (opts *SearchOpts) RunRipgrep(ctx context.Context, rg *exec.Cmd) ([]*SearchResult, error) {
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
	results, err := opts.parseRgOutput(ctx, out)
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

func (opts *SearchOpts) parseRgOutput(ctx context.Context, rgOut io.ReadCloser) ([]*SearchResult, error) {
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
			fmt.Fprintln(stdio.Stdout, line)
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
			FilePath:     data.Path.Text,
			LineNumber:   data.LineNumber,
			MatchedLines: data.Lines.Text,
		}

		ensureLn := result.MatchedLines
		if !strings.HasSuffix(ensureLn, "\n") {
			ensureLn += "\n"
		}
		fmt.Fprintf(stdio.Stdout, "%s:%d:%s", result.FilePath, result.LineNumber, ensureLn)

		results = append(results, result)
		if opts.Limit != nil && len(results) >= *opts.Limit {
			break
		}
	}
	return results, nil
}
