package hotline

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"strings"
	"unicode"
)

const MaxAttachmentBytes = 1024 * 1024
const MaxAttachments = 100

// File is a selected path and its contents at send time, not a live file link.
type File struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

type Attachment struct {
	Path string `json:"path"`
	Size int    `json:"size"`
}

func ValidateFiles(files []File) error {
	if len(files) > MaxAttachments {
		return problem(400, "invalid_input", "Attach at most 100 files.")
	}
	total := 0
	seen := map[string]bool{}
	for _, file := range files {
		if !fs.ValidPath(file.Path) || file.Path == "." || len(file.Path) > 512 || strings.ContainsAny(file.Path, "\\:") || strings.IndexFunc(file.Path, unicode.IsControl) >= 0 {
			return problem(400, "invalid_input", "Use relative workspace file paths without parent traversal or control characters.")
		}
		if seen[file.Path] {
			return problem(400, "invalid_input", "Attach each path only once.")
		}
		seen[file.Path] = true
		total += len(file.Data)
		if total > MaxAttachmentBytes {
			return problem(400, "invalid_input", "Attachments must total at most 1 MiB.")
		}
	}
	return nil
}

func bundleFiles(files []File) ([]byte, error) {
	if len(files) == 0 {
		return nil, problem(404, "not_found", "Attachment bundle not found.")
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, file := range files {
		w, err := z.Create(file.Path)
		if err != nil {
			return nil, err
		}
		if _, err = w.Write(file.Data); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
