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

package memory

import (
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/containerd/stargz-snapshotter/estargz"
	"github.com/containerd/stargz-snapshotter/metadata"
	digest "github.com/opencontainers/go-digest"
)

type reader struct {
	r      *estargz.Reader
	rootID uint32

	idMap     map[uint32]*estargz.TOCEntry
	idOfEntry map[string]uint32

	estargzOpts []estargz.OpenOption
}

func newReader(er *estargz.Reader, rootID uint32, idMap map[uint32]*estargz.TOCEntry, idOfEntry map[string]uint32, estargzOpts []estargz.OpenOption) *reader {
	return &reader{r: er, rootID: rootID, idMap: idMap, idOfEntry: idOfEntry, estargzOpts: estargzOpts}
}

func NewReader(sr *io.SectionReader, opts ...metadata.Option) (metadata.Reader, error) {
	var rOpts metadata.Options
	for _, o := range opts {
		if err := o(&rOpts); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	telemetry := &estargz.Telemetry{}
	if rOpts.Telemetry != nil {
		telemetry.GetFooterLatency = estargz.MeasureLatencyHook(rOpts.Telemetry.GetFooterLatency)
		telemetry.GetTocLatency = estargz.MeasureLatencyHook(rOpts.Telemetry.GetTocLatency)
		telemetry.DeserializeTocLatency = estargz.MeasureLatencyHook(rOpts.Telemetry.DeserializeTocLatency)
	}
	var decompressors []estargz.Decompressor
	for _, d := range rOpts.Decompressors {
		decompressors = append(decompressors, d)
	}

	erOpts := []estargz.OpenOption{
		estargz.WithTOCOffset(rOpts.TOCOffset),
		estargz.WithTelemetry(telemetry),
		estargz.WithDecompressors(decompressors...),
	}
	er, err := estargz.Open(sr, erOpts...)
	if err != nil {
		return nil, err
	}
	root, ok := er.Lookup("")
	if !ok {
		return nil, fmt.Errorf("failed to get root node")
	}
	rootID, idMap, idOfEntry, err := assignIDs(er, root)
	if err != nil {
		return nil, err
	}
	r := newReader(er, rootID, idMap, idOfEntry, erOpts)
	return r, nil
}

// assignIDs assigns an to each TOC item and returns a mapping from ID to entry and vice-versa.
func assignIDs(er *estargz.Reader, e *estargz.TOCEntry) (rootID uint32, idMap map[uint32]*estargz.TOCEntry, idOfEntry map[string]uint32, err error) {
	idMap = make(map[uint32]*estargz.TOCEntry)
	idOfEntry = make(map[string]uint32)
	curID := uint32(0)

	nextID := func() (uint32, error) {
		if curID == math.MaxUint32 {
			return 0, fmt.Errorf("sequence id too large")
		}
		curID++
		return curID, nil
	}

	var mapChildren func(e *estargz.TOCEntry) (uint32, error)
	mapChildren = func(e *estargz.TOCEntry) (uint32, error) {
		if e.Type == "hardlink" {
			return 0, fmt.Errorf("unexpected type \"hardlink\": this should be replaced to the destination entry")
		}

		var ok bool
		id, ok := idOfEntry[e.Name]
		if !ok {
			id, err = nextID()
			if err != nil {
				return 0, err
			}
			idMap[id] = e
			idOfEntry[e.Name] = id
		}

		e.ForeachChild(func(_ string, ent *estargz.TOCEntry) bool {
			_, err = mapChildren(ent)
			return err == nil
		})
		if err != nil {
			return 0, err
		}
		return id, nil
	}

	rootID, err = mapChildren(e)
	if err != nil {
		return 0, nil, nil, err
	}

	return rootID, idMap, idOfEntry, nil
}

func (r *reader) RootID() uint32 {
	return r.rootID
}

func (r *reader) TOCDigest() digest.Digest {
	return r.r.TOCDigest()
}

func (r *reader) GetOffset(id uint32) (offset int64, err error) {
	e, ok := r.idMap[id]
	if !ok {
		return 0, fmt.Errorf("entry %d not found", id)
	}
	return e.Offset, nil
}

func (r *reader) GetAttr(id uint32) (attr metadata.Attr, err error) {
	e, ok := r.idMap[id]
	if !ok {
		err = fmt.Errorf("entry %d not found", id)
		return
	}
	// TODO: zero copy
	attrFromTOCEntry(e, &attr)
	return
}

func (r *reader) GetChild(pid uint32, base string) (id uint32, attr metadata.Attr, err error) {
	e, ok := r.idMap[pid]
	if !ok {
		err = fmt.Errorf("parent entry %d not found", pid)
		return
	}
	child, ok := e.LookupChild(base)
	if !ok {
		err = fmt.Errorf("child %q of entry %d not found", base, pid)
		return
	}
	cid, ok := r.idOfEntry[child.Name]
	if !ok {
		err = fmt.Errorf("id of entry %q not found", base)
		return
	}
	// TODO: zero copy
	attrFromTOCEntry(child, &attr)
	return cid, attr, nil
}

func (r *reader) ForeachChild(id uint32, f func(name string, id uint32, mode os.FileMode) bool) error {
	e, ok := r.idMap[id]
	if !ok {
		return fmt.Errorf("parent entry %d not found", id)
	}
	var err error
	e.ForeachChild(func(baseName string, ent *estargz.TOCEntry) bool {
		id, ok := r.idOfEntry[ent.Name]
		if !ok {
			err = fmt.Errorf("id of child entry %q not found", baseName)
			return false
		}
		return f(baseName, id, ent.Stat().Mode())
	})
	return err
}

func (r *reader) OpenFile(id uint32) (metadata.File, error) {
	e, ok := r.idMap[id]
	if !ok {
		return nil, fmt.Errorf("entry %d not found", id)
	}
	sr, err := r.r.OpenFile(e.Name)
	if err != nil {
		return nil, err
	}
	return &file{r, e, sr}, nil
}

func (r *reader) OpenFileWithPreReader(id uint32, preRead func(id uint32, chunkOffset, chunkSize int64, chunkDigest string, r io.Reader) error) (metadata.File, error) {
	e, ok := r.idMap[id]
	if !ok {
		return nil, fmt.Errorf("entry %d not found", id)
	}
	sr, err := r.r.OpenFileWithPreReader(e.Name, func(e *estargz.TOCEntry, chunkR io.Reader) error {
		cid, ok := r.idOfEntry[e.Name]
		if !ok {
			return fmt.Errorf("id of entry %q not found", e.Name)
		}
		return preRead(cid, e.ChunkOffset, e.ChunkSize, e.ChunkDigest, chunkR)
	})
	if err != nil {
		return nil, err
	}
	return &file{r, e, sr}, nil
}

func (r *reader) Clone(sr *io.SectionReader) (metadata.Reader, error) {
	er, err := estargz.Open(sr, r.estargzOpts...)
	if err != nil {
		return nil, err
	}

	return newReader(er, r.rootID, r.idMap, r.idOfEntry, r.estargzOpts), nil
}

func (r *reader) Close() error {
	return nil
}

type file struct {
	r  *reader
	e  *estargz.TOCEntry
	sr *io.SectionReader
}

func (r *file) ChunkEntryForOffset(offset int64) (off int64, size int64, dgst string, ok bool) {
	e, ok := r.r.r.ChunkEntryForOffset(r.e.Name, offset)
	if !ok {
		return 0, 0, "", false
	}
	dgst = e.Digest
	if e.ChunkDigest != "" {
		// NOTE* "reg" also can contain ChunkDigest (e.g. when "reg" is the first entry of
		// chunked file)
		dgst = e.ChunkDigest
	}
	return e.ChunkOffset, e.ChunkSize, dgst, true
}

func (r *file) ReadAt(p []byte, off int64) (n int, err error) {
	return r.sr.ReadAt(p, off)
}

func (r *reader) NumOfNodes() (i int, _ error) {
	return len(r.idMap), nil
}

// TODO: share it with db pkg
func attrFromTOCEntry(src *estargz.TOCEntry, dst *metadata.Attr) *metadata.Attr {
	dst.Size = src.Size
	dst.ModTime, _ = time.Parse(time.RFC3339, src.ModTime3339)
	dst.LinkName = src.LinkName
	dst.Mode = src.Stat().Mode()
	dst.UID = src.UID
	dst.GID = src.GID
	dst.DevMajor = src.DevMajor
	dst.DevMinor = src.DevMinor
	dst.Xattrs = src.Xattrs
	dst.NumLink = src.NumLink
	return dst
}
