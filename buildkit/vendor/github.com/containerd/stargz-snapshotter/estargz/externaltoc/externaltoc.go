/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

/*
   Copyright 2019 The Go Authors. All rights reserved.
   Use of this source code is governed by a BSD-style
   license that can be found in the LICENSE file.
*/

package externaltoc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"sync"

	"github.com/containerd/stargz-snapshotter/estargz"
	digest "github.com/opencontainers/go-digest"
)

type GzipCompression struct {
	*GzipCompressor
	*GzipDecompressor
}

func NewGzipCompressionWithLevel(provideTOC func() ([]byte, error), level int) estargz.Compression {
	return &GzipCompression{
		NewGzipCompressorWithLevel(level),
		NewGzipDecompressor(provideTOC),
	}
}

func NewGzipCompressor() *GzipCompressor {
	return &GzipCompressor{compressionLevel: gzip.BestCompression}
}

func NewGzipCompressorWithLevel(level int) *GzipCompressor {
	return &GzipCompressor{compressionLevel: level}
}

type GzipCompressor struct {
	compressionLevel int
	buf              *bytes.Buffer
}

func (gc *GzipCompressor) WriteTOCTo(w io.Writer) (int, error) {
	if len(gc.buf.Bytes()) == 0 {
		return 0, fmt.Errorf("TOC hasn't been registered")
	}
	return w.Write(gc.buf.Bytes())
}

func (gc *GzipCompressor) Writer(w io.Writer) (estargz.WriteFlushCloser, error) {
	return gzip.NewWriterLevel(w, gc.compressionLevel)
}

func (gc *GzipCompressor) WriteTOCAndFooter(w io.Writer, off int64, toc *estargz.JTOC, diffHash hash.Hash) (digest.Digest, error) {
	tocJSON, err := json.MarshalIndent(toc, "", "\t")
	if err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	gz, _ := gzip.NewWriterLevel(buf, gc.compressionLevel)
	// TOC isn't written to layer so no effect to diff ID
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     estargz.TOCTarName,
		Size:     int64(len(tocJSON)),
	}); err != nil {
		return "", err
	}
	if _, err := tw.Write(tocJSON); err != nil {
		return "", err
	}

	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	gc.buf = buf
	footerBytes, err := gzipFooterBytes()
	if err != nil {
		return "", err
	}
	if _, err := w.Write(footerBytes); err != nil {
		return "", err
	}
	return digest.FromBytes(tocJSON), nil
}

// The footer is an empty gzip stream with no compression and an Extra header.
//
// 46 comes from:
//
// 10 bytes  gzip header
// 2  bytes  XLEN (length of Extra field) = 21 (4 bytes header + len("STARGZEXTERNALTOC"))
// 2  bytes  Extra: SI1 = 'S', SI2 = 'G'
// 2  bytes  Extra: LEN = 17 (len("STARGZEXTERNALTOC"))
// 17 bytes  Extra: subfield = "STARGZEXTERNALTOC"
// 5  bytes  flate header
// 8  bytes  gzip footer
// (End of the eStargz blob)
const FooterSize = 46

// gzipFooterBytes returns the 104 bytes footer.
func gzipFooterBytes() ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, FooterSize))
	gz, _ := gzip.NewWriterLevel(buf, gzip.NoCompression) // MUST be NoCompression to keep 51 bytes

	// Extra header indicating the offset of TOCJSON
	// https://tools.ietf.org/html/rfc1952#section-2.3.1.1
	header := make([]byte, 4)
	header[0], header[1] = 'S', 'G'
	subfield := "STARGZEXTERNALTOC"                                   // len("STARGZEXTERNALTOC") = 17
	binary.LittleEndian.PutUint16(header[2:4], uint16(len(subfield))) // little-endian per RFC1952
	gz.Header.Extra = append(header, []byte(subfield)...)
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if buf.Len() != FooterSize {
		panic(fmt.Sprintf("footer buffer = %d, not %d", buf.Len(), FooterSize))
	}
	return buf.Bytes(), nil
}

func NewGzipDecompressor(provideTOCFunc func() ([]byte, error)) *GzipDecompressor {
	return &GzipDecompressor{provideTOCFunc: provideTOCFunc}
}

type GzipDecompressor struct {
	provideTOCFunc func() ([]byte, error)
	rawTOC         []byte // Do not access this field directly. Get this through getTOC() method.
	getTOCOnce     sync.Once
}

func (gz *GzipDecompressor) getTOC() ([]byte, error) {
	if len(gz.rawTOC) == 0 {
		var retErr error
		gz.getTOCOnce.Do(func() {
			if gz.provideTOCFunc == nil {
				retErr = fmt.Errorf("TOC hasn't been provided")
				return
			}
			rawTOC, err := gz.provideTOCFunc()
			if err != nil {
				retErr = err
				return
			}
			gz.rawTOC = rawTOC
		})
		if retErr != nil {
			return nil, retErr
		}
		if len(gz.rawTOC) == 0 {
			return nil, fmt.Errorf("no TOC is provided")
		}
	}
	return gz.rawTOC, nil
}

func (gz *GzipDecompressor) Reader(r io.Reader) (io.ReadCloser, error) {
	return gzip.NewReader(r)
}

func (gz *GzipDecompressor) ParseTOC(r io.Reader) (toc *estargz.JTOC, tocDgst digest.Digest, err error) {
	if r != nil {
		return nil, "", fmt.Errorf("TOC must be provided externally but got internal one")
	}
	rawTOC, err := gz.getTOC()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get TOC: %v", err)
	}
	return parseTOCEStargz(bytes.NewReader(rawTOC))
}

func (gz *GzipDecompressor) ParseFooter(p []byte) (blobPayloadSize, tocOffset, tocSize int64, err error) {
	if len(p) != FooterSize {
		return 0, 0, 0, fmt.Errorf("invalid length %d cannot be parsed", len(p))
	}
	zr, err := gzip.NewReader(bytes.NewReader(p))
	if err != nil {
		return 0, 0, 0, err
	}
	defer zr.Close()
	extra := zr.Header.Extra
	si1, si2, subfieldlen, subfield := extra[0], extra[1], extra[2:4], extra[4:]
	if si1 != 'S' || si2 != 'G' {
		return 0, 0, 0, fmt.Errorf("invalid subfield IDs: %q, %q; want E, S", si1, si2)
	}
	if slen := binary.LittleEndian.Uint16(subfieldlen); slen != uint16(len("STARGZEXTERNALTOC")) {
		return 0, 0, 0, fmt.Errorf("invalid length of subfield %d; want %d", slen, 16+len("STARGZ"))
	}
	if string(subfield) != "STARGZEXTERNALTOC" {
		return 0, 0, 0, fmt.Errorf("STARGZ magic string must be included in the footer subfield")
	}
	// tocOffset < 0 indicates external TOC.
	// blobPayloadSize < 0 indicates the entire blob size.
	return -1, -1, 0, nil
}

func (gz *GzipDecompressor) FooterSize() int64 {
	return FooterSize
}

func (gz *GzipDecompressor) DecompressTOC(r io.Reader) (tocJSON io.ReadCloser, err error) {
	if r != nil {
		return nil, fmt.Errorf("TOC must be provided externally but got internal one")
	}
	rawTOC, err := gz.getTOC()
	if err != nil {
		return nil, fmt.Errorf("failed to get TOC: %v", err)
	}
	return decompressTOCEStargz(bytes.NewReader(rawTOC))
}

func parseTOCEStargz(r io.Reader) (toc *estargz.JTOC, tocDgst digest.Digest, err error) {
	tr, err := decompressTOCEStargz(r)
	if err != nil {
		return nil, "", err
	}
	dgstr := digest.Canonical.Digester()
	toc = new(estargz.JTOC)
	if err := json.NewDecoder(io.TeeReader(tr, dgstr.Hash())).Decode(&toc); err != nil {
		return nil, "", fmt.Errorf("error decoding TOC JSON: %v", err)
	}
	if err := tr.Close(); err != nil {
		return nil, "", err
	}
	return toc, dgstr.Digest(), nil
}

func decompressTOCEStargz(r io.Reader) (tocJSON io.ReadCloser, err error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("malformed TOC gzip header: %v", err)
	}
	zr.Multistream(false)
	tr := tar.NewReader(zr)
	h, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("failed to find tar header in TOC gzip stream: %v", err)
	}
	if h.Name != estargz.TOCTarName {
		return nil, fmt.Errorf("TOC tar entry had name %q; expected %q", h.Name, estargz.TOCTarName)
	}
	return readCloser{tr, zr.Close}, nil
}

type readCloser struct {
	io.Reader
	closeFunc func() error
}

func (rc readCloser) Close() error {
	return rc.closeFunc()
}
