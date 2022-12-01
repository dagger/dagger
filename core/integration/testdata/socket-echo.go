package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: %s <network> <addr> <message>", os.Args[0])
		os.Exit(1)
		return
	}

	c, err := net.Dial(os.Args[1], os.Args[2])
	if err != nil {
		panic(err)
	}

	defer c.Close()

	_, err = fmt.Fprintln(c, os.Args[3])
	if err != nil {
		panic(err)
	}

	var res string
	n, err := fmt.Fscanln(c, &res)
	if err != nil && n == 0 {
		panic(err)
	}

	fmt.Println(res)
}
