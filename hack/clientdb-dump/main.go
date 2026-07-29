// clientdb-dump decodes a closed client's append-only telemetry files to JSONL.
//
// Usage:
//
//	go run ./hack/clientdb-dump -root /path/to/clientdbs -client CLIENT_ID -stream all
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/dagger/dagger/engine/clientdb"
)

var (
	root     = flag.String("root", "", "directory containing client telemetry stores")
	clientID = flag.String("client", "", "client ID to dump")
	stream   = flag.String("stream", clientdb.DumpAll, "stream to dump: all, spans, logs, or metrics")
)

func main() {
	flag.Parse()
	if *root == "" || *clientID == "" {
		fmt.Fprintln(os.Stderr, "-root and -client are required")
		flag.Usage()
		os.Exit(2)
	}
	if err := clientdb.Dump(context.Background(), *root, *clientID, *stream, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
