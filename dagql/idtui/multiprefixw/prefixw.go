package multiprefixw

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

type Writer struct {
	w io.Writer

	Prefix       string
	LineOverhang string

	wrote          bool
	lineTerminated bool
	lastPrefix     string
}

func New(w io.Writer) *Writer {
	return &Writer{w: w, LineOverhang: DefaultLineOverhang}
}

const DefaultLineOverhang = "\u23CE" // ⏎

func (pw *Writer) Write(p []byte) (int, error) {
	for len(p) > 0 {
		n := bytes.IndexByte(p, '\n')
		if n < 0 {
			if err := pw.writePrefix(pw.Prefix); err != nil {
				return 0, err
			}
			if _, err := pw.w.Write(p); err != nil {
				return 0, err
			}
			pw.wrote = true
			pw.lineTerminated = false
			break
		}
		if err := pw.writePrefix(pw.Prefix); err != nil {
			return 0, err
		}
		if _, err := pw.w.Write(p[:n+1]); err != nil {
			return 0, err
		}
		pw.wrote = true
		p = p[n+1:]
		pw.lineTerminated = true
	}
	return len(p), nil
}

func (pw *Writer) writePrefix(prefix string) error {
	isHeader := strings.HasSuffix(prefix, "\n")
	wasHeader := strings.HasSuffix(pw.lastPrefix, "\n")
	if pw.lastPrefix == prefix && !pw.lineTerminated {
		// same prefix and we're not a new line, so keep on going
		return nil
	}
	if pw.wrote && pw.lastPrefix != prefix && !pw.lineTerminated {
		// new prefix and last line was not terminated, so manually add linebreak
		if _, err := fmt.Fprint(pw.w, pw.LineOverhang+"\n"); err != nil {
			return err
		}
	}
	if isHeader || wasHeader {
		if pw.lastPrefix == prefix {
			// the prefix spans multiple lines, so we don't need a prefix for this line
			return nil
		} else if pw.wrote {
			// ensure a gap from previous output
			fmt.Fprintln(pw.w)
		}
	}
	// write our prefix and remember it
	_, err := fmt.Fprint(pw.w, prefix)
	pw.lastPrefix = prefix
	return err
}
