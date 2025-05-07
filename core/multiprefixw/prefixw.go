package multiprefixw

import (
	"bytes"
	"fmt"
	"io"
)

type Writer struct {
	w              io.Writer
	prefix         string
	lastPrefix     string
	lineTerminated bool
}

func New(w io.Writer) *Writer {
	return &Writer{w: w}
}

func (pw *Writer) SetPrefix(prefix string) {
	pw.prefix = prefix
}

func (pw *Writer) Write(p []byte) (int, error) {
	for len(p) > 0 {
		n := bytes.IndexByte(p, '\n')
		if n < 0 {
			if err := pw.writePrefix(pw.prefix); err != nil {
				return 0, err
			}
			if _, err := pw.w.Write(p); err != nil {
				return 0, err
			}
			pw.lineTerminated = false
			break
		}
		if err := pw.writePrefix(pw.prefix); err != nil {
			return 0, err
		}
		if _, err := pw.w.Write(p[:n+1]); err != nil {
			return 0, err
		}
		p = p[n+1:]
		pw.lineTerminated = true
	}
	return len(p), nil
}

func (pw *Writer) writePrefix(prefix string) error {
	if pw.lastPrefix == prefix && !pw.lineTerminated {
		// same prefix and we're not a new line, so keep on going
		return nil
	}
	if pw.lastPrefix != "" && pw.lastPrefix != prefix && !pw.lineTerminated {
		// new prefix and last line was not terminated, so manually add linebreak
		if _, err := fmt.Fprint(pw.w, "\u23CE\n"); err != nil { // ⏎
			return err
		}
	}
	// write our prefix and remember it
	_, err := fmt.Fprint(pw.w, prefix)
	pw.lastPrefix = prefix
	return err
}
