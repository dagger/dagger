package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"

	"github.com/pkg/errors"
)

type TarItem struct {
	Header *tar.Header
	Data   []byte
}

func ReadTarToMap(dt []byte, compressed bool) (map[string]*TarItem, error) {
	m := map[string]*TarItem{}
	var r io.Reader = bytes.NewBuffer(dt)
	if compressed {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, errors.Wrapf(err, "error creating gzip reader")
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				return m, nil
			}
			return nil, errors.Wrap(err, "error reading tar")
		}
		if _, ok := m[h.Name]; ok {
			return nil, errors.Errorf("duplicate entries for %s", h.Name)
		}

		var dt []byte
		if h.Typeflag == tar.TypeReg {
			dt, err = io.ReadAll(tr)
			if err != nil {
				return nil, errors.Wrapf(err, "error reading file")
			}
		}
		m[h.Name] = &TarItem{Header: h, Data: dt}
	}
}
