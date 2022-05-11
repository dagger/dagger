package testutil

import (
	"fmt"
	"log"
	"os"
)

func OKResultFile(filename string) error {
	// first, we clean a file if it exists in the first place
	err := os.Remove(filename)
	if err != nil {
		log.Printf("could not clean up test result file '%s': %v", filename, err)
	}

	var f *os.File
	f, err = os.Create(filename)
	if err != nil {
		return fmt.Errorf("can not create test result file: %w", err)
	}
	defer func() {
		err = f.Close()
		if err != nil {
			err = fmt.Errorf("can not close test result file: %w", err)
		}
	}()

	_, err = f.Write([]byte("OK"))
	if err != nil {
		return fmt.Errorf("can not write test result file: %w", err)
	}

	// can be set by the deferred f.Close()
	return err
}
