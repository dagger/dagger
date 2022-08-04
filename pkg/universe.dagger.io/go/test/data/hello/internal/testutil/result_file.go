package testutil

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/mitchellh/go-homedir"
)

func OKResultFile(tmpDirPrefix, filename string) (string, error) {
	id, err := uuid.NewUUID()
	if err != nil {
		return "", err
	}

	testDir, err := homedir.Expand("~/test/" + tmpDirPrefix + id.String())
	if err != nil {
		return "", err
	}

	err = os.MkdirAll(testDir, 0o755)
	if err != nil {
		return "", err
	}

	//tmpDir, err := os.MkdirTemp("/mnt", tmpDirPrefix)
	//if err != nil {
	//	return "", fmt.Errorf("can not create test temp dir: %w", err)
	//}

	// first, we clean a file if it exists in the first place
	//err := os.Remove(filename)
	//if err != nil {
	//	log.Printf("could not clean up test result file '%s': %v", filename, err)
	//}

	var f *os.File
	//testFile := filepath.Join(tmpDir, filename)
	testFile := filepath.Join(testDir, filename)
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

func FindTmp() error {

	home, err := homedir.Expand("~/test")
	if err != nil {
		return err
	}
	log.Printf("writing to %q", home)

	err = os.MkdirAll(home, 0o755)
	if err != nil {
		return err
	}

	rootFS := os.DirFS(home)
	err = fs.WalkDir(rootFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(path)
		return nil
	})
	return err
}
