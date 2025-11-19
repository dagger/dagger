package main

import (
	"context"
	"flag"
	"os"
	"strings"

	"github.com/tonistiigi/fsutil"
)

func main() {
	flag.Parse()
	if len(flag.Args()) == 0 {
		panic("source path not set")
	}

	var excludes []string

	if len(flag.Args()) > 1 {
		dt, err := os.ReadFile(flag.Args()[1])
		if err != nil {
			panic(err)
		}
		excludes = strings.Split(string(dt), "\n")
	}

	if err := fsutil.Walk(context.Background(), flag.Args()[0], &fsutil.FilterOpt{
		ExcludePatterns: excludes,
	}, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		panic(err)
	}
}
