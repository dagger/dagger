// Package main provides simple consistenthash commandline utility.
/*
Demo:

  $ seq -f node-%.f 1 100 > /tmp/nodes
  $ consistenthash < /tmp/nodes apple
  node-42
  $ consistenthash < /tmp/nodes banana
  node-48
  $ consistenthash < /tmp/nodes chocolate
  node-2

Modify /tmp/nodes and confirm that the result rarely changes.
*/
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/serialx/hashring"
	"github.com/sirupsen/logrus"
)

func main() {
	if err := xmain(); err != nil {
		logrus.Fatal(err)
	}
}

func xmain() error {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s KEY\nNode list needs to be provided via stdin\n", os.Args[0])
		os.Exit(1)
		return errors.New("should not reach here")
	}
	key := os.Args[1]
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	var nodes []string
	for _, s := range strings.Split(string(stdin), "\n") {
		s = strings.TrimSpace(s)
		if s != "" {
			nodes = append(nodes, s)
		}
	}
	chosen, err := doConsistentHash(nodes, key)
	if err != nil {
		return err
	}
	fmt.Println(chosen)
	return nil
}

func doConsistentHash(nodes []string, key string) (string, error) {
	ring := hashring.New(nodes)
	x, ok := ring.GetNode(key)
	if !ok {
		return "", errors.Errorf("no node found for key %q", key)
	}
	return x, nil
}
