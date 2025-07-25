package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/muesli/reflow/indent"
	"github.com/muesli/termenv"
)

func shellDebugLine(title string, data ...any) string {
	lvl := "[DBG]"

	sb := new(strings.Builder)
	sb.WriteString(termenv.String(lvl).Foreground(termenv.ANSIMagenta).String())
	sb.WriteString(" ")
	sb.WriteString(termenv.String(title).Bold().Faint().String())

	extra := new(strings.Builder)
	if len(data) > 0 {
		// if first element is a string, display it next to title
		if s, ok := data[0].(string); ok {
			extra.WriteString(s)
			data = data[1:]
		}
		// for other values, display each in a new arrowed line
		iw := indent.NewWriter(uint(len(lvl)+1), nil)
		for _, arg := range data {
			if arg != nil {
				fmt.Fprintf(iw, "\n→ %s", shellDebugFormat(arg))
			}
		}
		extra.WriteString(iw.String())
	}

	// style everything faint so normal output stands out more
	if extra.Len() > 0 {
		sb.WriteString(" ")
		sb.WriteString(termenv.String(extra.String()).Faint().String())
	}

	sb.WriteString("\n")

	return sb.String()
}

func shellDebugFormat(data any) string {
	switch t := data.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case error:
		return fmt.Sprintf("Error: %s", t.Error())
	case *ShellState:
		if t == nil {
			return "State: <nil>"
		}
		return shellDebugFormat(*t)
	case ShellState:
		if t.IsError() {
			return shellDebugFormat(t.Error)
		}
		r := fmt.Sprintf("[key=%s]", t.Key)
		if t.ModDigest != "" {
			r += fmt.Sprintf(" [module=%s]", t.ModDigest)
		}
		if t.Cmd != "" {
			r += fmt.Sprintf(" [namespace=%s]", t.Cmd)
		}
		if len(t.Calls) > 0 {
			r += ":\n" + shellDebugFormat(t.Calls)
		}
		return fmt.Sprintf("State %s", r)
	default:
		b, _ := json.MarshalIndent(t, "", "  ")
		return string(b)
	}
}

func shellDebug(ctx context.Context, title string, data ...any) {
	msg := shellDebugLine(title, data...)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	fmt.Fprint(stdio.Stderr, msg)
}
