// Package main sleeps for the given number of seconds — a wall-clock spacer
// for QA repros that need a TTL to lapse (scratch tool, lives under
// gitignored tmp/).
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	secs := 60
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil {
			secs = n
		}
	}
	fmt.Printf("sleeping %ds...\n", secs)
	time.Sleep(time.Duration(secs) * time.Second)
	fmt.Println("done")
}
