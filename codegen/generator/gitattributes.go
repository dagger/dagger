package generator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/psanford/memfs"
)

const GitAttributesFile = ".gitattributes"

func InstallGitAttributes(mfs *memfs.FS, fileName, outDir string) error {
	content := []byte(fmt.Sprintf("/%s linguist-generated=true\n", fileName))
	if existing, err := os.ReadFile(filepath.Join(outDir, GitAttributesFile)); err == nil {
		if bytes.Contains(existing, []byte(fileName)) {
			// already has some config for the file
			return nil
		}

		if !bytes.HasSuffix(existing, []byte("\n")) {
			existing = append(existing, '\n')
		}

		content = append(existing, content...)
	}

	return mfs.WriteFile(GitAttributesFile, content, 0600)
}
