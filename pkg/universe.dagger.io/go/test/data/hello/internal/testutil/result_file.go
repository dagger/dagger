package testutil

import (
	"fmt"
	"os"
	"path/filepath"
)

func OKResultFile(tmpDirPrefix, filename string) (string, error) {
	tmpDir, err := os.MkdirTemp("/tmp", tmpDirPrefix)
	if err != nil {
		return "", fmt.Errorf("can not create test temp dir: %w", err)
	}

	// first, we clean a file if it exists in the first place
	//err := os.Remove(filename)
	//if err != nil {
	//	log.Printf("could not clean up test result file '%s': %v", filename, err)
	//}

	var f *os.File
	testFile := filepath.Join(tmpDir, filename)
	f, err = os.Create(testFile)
	if err != nil {
		return "", fmt.Errorf("can not create test result file: %w", err)
	}
	defer func() {
		err = f.Close()
		if err != nil {
			err = fmt.Errorf("can not close test result file: %w", err)
		}
	}()

	_, err = f.Write([]byte("OK"))
	if err != nil {
		return "", fmt.Errorf("can not write test result file: %w", err)
	}

	// can be set by the deferred f.Close()
	return testFile, err
}
