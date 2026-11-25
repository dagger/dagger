package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/dagger/dagger/dagql"
)

// A set file is one file the service publishes of a check's set: the
// results that check's runs used, which an engine claimed for the check
// merges as it would a merge's bundles. The service writes it; the engine
// reads it. It is one line of JSON, its header (SetFileHeader), then its
// parts back to back, each exactly as many bytes as the header says. A part
// is one zstd frame of the JSON of a closed dagql.ValueBundle, with its
// layers listed once as a merge's is (Merge.MarshalJSON), and no download
// addresses: a table beside the file gives them. A file is named by the
// digest of its bytes.

// SetFileHeader is a set file's first line.
type SetFileHeader struct {
	// Version is the bundle version of every part (dagql.ValueBundleVersion).
	// An engine reads no part of a file of another version.
	Version int `json:"version"`
	// Parts are the file's parts, in the order they follow the header and
	// are merged.
	Parts []SetFilePart `json:"parts"`

	// The rest is for logs: the set's job and line, which file of the set
	// it is ("head" or "whole"), and when its build began.
	Workspace string    `json:"workspace,omitempty"`
	Module    string    `json:"module,omitempty"`
	Check     string    `json:"check,omitempty"`
	Line      string    `json:"line,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Built     time.Time `json:"built,omitzero"`
}

// SetFilePart is one part of a set file: its length in the file, compressed,
// how many records its bundle carries, and those of its bundle's roots the
// engine merges unretained (PreparedValueMerge.Unretained): records a part
// carries ahead of the root whose closure they are in, which a later part
// of the file carries, and which live meanwhile only as long as the
// restore holds them.
type SetFilePart struct {
	Bytes      int64                   `json:"bytes"`
	Records    int                     `json:"records"`
	Unretained []dagql.TransferOrdinal `json:"unretained,omitempty"`
	// Fetch are parts of the part's records that the set's runs read
	// (dagql.PartRead with a session), for the engine to fetch ahead once it
	// has merged the part (dagql.Cache.PlanPartFetch).
	Fetch []SetFileFetch `json:"fetch,omitempty"`
}

// SetFileFetch is one part to fetch ahead, at once: the record of the part's
// bundle with Ordinal, and its part Part, as a read names it (at any of the
// record's output paths), or the whole record when Part is empty.
type SetFileFetch struct {
	Ordinal dagql.TransferOrdinal `json:"ordinal"`
	Part    dagql.PartKey         `json:"part,omitempty"`
}

// EncodeSetFilePart encodes a bundle as a set file's part with enc, and
// returns the part and what the header says of it. The part's JSON is
// written once, into buf, and compressed from there: give every part of a
// file the same buf, which then grows once, to the file's largest part.
func EncodeSetFilePart(enc *zstd.Encoder, buf *bytes.Buffer, bundle dagql.ValueBundle) ([]byte, SetFilePart, error) {
	shared, err := dagql.ShareChainLayers(bundle)
	if err != nil {
		return nil, SetFilePart{}, err
	}
	buf.Reset()
	if err := marshalInto(buf, shared); err != nil {
		return nil, SetFilePart{}, err
	}
	part := enc.EncodeAll(buf.Bytes(), nil)
	return part, SetFilePart{Bytes: int64(len(part)), Records: len(bundle.Values)}, nil
}

// SetFileBuffers returns a set file of the parts, each as EncodeSetFilePart
// made it with its header entry in header.Parts at the same index, as the
// buffers it is made of: its header line, then the parts themselves, which
// it doesn't copy.
func SetFileBuffers(header SetFileHeader, parts [][]byte) (net.Buffers, error) {
	if len(parts) != len(header.Parts) {
		return nil, fmt.Errorf("set file: %d parts and %d in the header", len(parts), len(header.Parts))
	}
	for i, part := range parts {
		if int64(len(part)) != header.Parts[i].Bytes {
			return nil, fmt.Errorf("set file: part %d is %d bytes and the header says %d", i, len(part), header.Parts[i].Bytes)
		}
	}
	line, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	return append(net.Buffers{append(line, '\n')}, parts...), nil
}

// WriteSetFile writes a set file of the parts (SetFileBuffers).
func WriteSetFile(w io.Writer, header SetFileHeader, parts [][]byte) error {
	file, err := SetFileBuffers(header, parts)
	if err != nil {
		return err
	}
	_, err = file.WriteTo(w)
	return err
}

// ReadSetFile splits a set file into its header and its parts, still
// compressed. The parts share data's memory.
func ReadSetFile(data []byte) (SetFileHeader, [][]byte, error) {
	var header SetFileHeader
	line, rest, ok := bytes.Cut(data, []byte{'\n'})
	if !ok {
		return header, nil, errors.New("set file: no header line")
	}
	if err := json.Unmarshal(line, &header); err != nil {
		return header, nil, fmt.Errorf("set file header: %w", err)
	}
	parts := make([][]byte, 0, len(header.Parts))
	for i, p := range header.Parts {
		if p.Bytes < 0 || p.Bytes > int64(len(rest)) {
			return header, nil, fmt.Errorf("set file: part %d of %d bytes, with %d left", i, p.Bytes, len(rest))
		}
		parts = append(parts, rest[:p.Bytes:p.Bytes])
		rest = rest[p.Bytes:]
	}
	if len(rest) != 0 {
		return header, nil, fmt.Errorf("set file: %d bytes after its last part", len(rest))
	}
	return header, parts, nil
}

// DecodeSetFilePart decodes one part of a set file with dec.
func DecodeSetFilePart(dec *zstd.Decoder, part []byte) (dagql.ValueBundle, error) {
	var bundle dagql.ValueBundle
	raw, err := dec.DecodeAll(part, nil)
	if err != nil {
		return bundle, fmt.Errorf("set file part: %w", err)
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return bundle, fmt.Errorf("set file part: %w", err)
	}
	return bundle, nil
}
