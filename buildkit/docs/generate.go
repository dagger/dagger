package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/pkg/errors"
)

func main() {
	re := regexp.MustCompile("(?s)<!---GENERATE_START (.*?)-->(.*?)<!---GENERATE_END-->\n")

	err := filepath.Walk("./docs", func(path string, stat fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if stat.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		dataNew := re.ReplaceAllFunc(data, func(match []byte) []byte {
			groups := re.FindStringSubmatch(string(match))
			stdout := bytes.NewBuffer(nil)
			fmt.Fprintf(stdout, "<!---GENERATE_START %s-->\n", groups[1])
			fmt.Fprintf(stdout, "```\n")
			cmd := exec.Cmd{
				Path:   "/bin/sh",
				Args:   []string{"sh", "-c", groups[1]},
				Stdout: stdout,
			}
			err = cmd.Start()
			if err != nil {
				err = errors.Wrapf(err, "could not start command %s", groups[1])
				return nil
			}
			err = cmd.Wait()
			if err != nil {
				err = errors.Wrapf(err, "could not run command %s", groups[1])
				return nil
			}
			fmt.Fprintf(stdout, "```\n")
			fmt.Fprintf(stdout, "<!---GENERATE_END-->\n")

			return stdout.Bytes()
		})
		if err != nil {
			return err
		}

		if !bytes.Equal(data, dataNew) {
			fmt.Println(path)
			if err := os.WriteFile(path, dataNew, stat.Mode()); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
