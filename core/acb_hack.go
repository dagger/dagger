package core

import (
	"fmt"
	"io"
	"os"
	"time"
)

type acbDump struct {
	w       *os.File
	doClose bool
}

func (a acbDump) Write(p []byte) (int, error) {
	return a.w.Write(p)
}

func (a acbDump) Close() error {
	if a.doClose {
		return a.w.Close()
	}
	return nil
}

func newACBDumpStdout() io.WriteCloser {
	return &acbDump{
		w: os.Stdout,
	}
}
func newACBDumpStderr() io.WriteCloser {
	return &acbDump{
		w: os.Stderr,
	}
}

func newACBDumpFile(p string) io.WriteCloser {
	w, err := os.Create(p)
	if err != nil {
		panic(err)
	}
	return &acbDump{
		w:       w,
		doClose: true,
	}
}

func newDumpFilePair() (io.WriteCloser, io.WriteCloser) {
	now := time.Now().UnixNano()
	stdoutPath := fmt.Sprintf("/tmp/%v.stdout", now)
	stderrPath := fmt.Sprintf("/tmp/%v.stderr", now)
	fmt.Printf("ACB Dumping to %s and %s\n", stdoutPath, stderrPath)
	return newACBDumpFile(stdoutPath), newACBDumpFile(stderrPath)
}
