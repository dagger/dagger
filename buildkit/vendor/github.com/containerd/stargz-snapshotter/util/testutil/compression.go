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

package testutil

import (
	"bytes"
	"io"

	"github.com/containerd/stargz-snapshotter/estargz"
	esgzexternaltoc "github.com/containerd/stargz-snapshotter/estargz/externaltoc"
	"github.com/containerd/stargz-snapshotter/estargz/zstdchunked"
	"github.com/klauspost/compress/zstd"
)

type Compression interface {
	estargz.Compressor
	estargz.Decompressor

	// DecompressTOC decompresses the passed blob and returns a reader of TOC JSON.
	// This is needed to be used from metadata pkg
	DecompressTOC(io.Reader) (tocJSON io.ReadCloser, err error)
}

type CompressionFactory func() Compression

type zstdCompression struct {
	*zstdchunked.Compressor
	*zstdchunked.Decompressor
}

func ZstdCompressionWithLevel(compressionLevel zstd.EncoderLevel) CompressionFactory {
	return func() Compression {
		return &zstdCompression{&zstdchunked.Compressor{CompressionLevel: compressionLevel}, &zstdchunked.Decompressor{}}
	}
}

type gzipCompression struct {
	*estargz.GzipCompressor
	*estargz.GzipDecompressor
}

func GzipCompressionWithLevel(compressionLevel int) CompressionFactory {
	return func() Compression {
		return gzipCompression{estargz.NewGzipCompressorWithLevel(compressionLevel), &estargz.GzipDecompressor{}}
	}
}

type externalTOCGzipCompression struct {
	*esgzexternaltoc.GzipCompressor
	*esgzexternaltoc.GzipDecompressor
}

func ExternalTOCGzipCompressionWithLevel(compressionLevel int) CompressionFactory {
	return func() Compression {
		compressor := esgzexternaltoc.NewGzipCompressorWithLevel(compressionLevel)
		decompressor := esgzexternaltoc.NewGzipDecompressor(func() ([]byte, error) {
			buf := new(bytes.Buffer)
			if _, err := compressor.WriteTOCTo(buf); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		})
		return &externalTOCGzipCompression{compressor, decompressor}
	}

}
