package main

import (
	"context"
	"flag"
	"os"

	"github.com/tonistiigi/fsutil"
	"github.com/tonistiigi/fsutil/util"
)

func main() {
	flag.Parse()
	if len(flag.Args()) == 0 {
		panic("source path not set")
	}

	ctx := context.Background()
	s := util.NewProtoStream(ctx, os.Stdin, os.Stdout)

	fs, err := fsutil.NewFS(flag.Args()[0])
	if err != nil {
		panic(err)
	}
	if err := fsutil.Send(ctx, s, fs, nil); err != nil {
		panic(err)
	}
}
