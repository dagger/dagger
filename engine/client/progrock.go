package client

import (
	"encoding/json"
	"os"

	"github.com/vito/progrock"
)

type progrockFileWriter struct {
	f   *os.File
	enc *json.Encoder
}

func newProgrockFileWriter(filePath string) (progrock.Writer, error) {
	f, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}

	enc := json.NewEncoder(f)

	return progrockFileWriter{
		f:   f,
		enc: enc,
	}, nil
}

func (w progrockFileWriter) WriteStatus(ev *progrock.StatusUpdate) error {
	return w.enc.Encode(ev)
}

func (w progrockFileWriter) Close() error {
	return w.f.Close()
}
