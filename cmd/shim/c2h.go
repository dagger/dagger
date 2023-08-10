package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
)

const upstreamSock = "/upstream.sock"

func tunnel(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tunnel <upstream> port/tcp")
	}

	log.Println("listening on", args[0])

	port, network, ok := strings.Cut(args[0], "/")
	if !ok {
		network = "tcp"
	}

	l, err := net.Listen(network, fmt.Sprintf(":%s", port))
	if err != nil {
		return err
	}

	for {
		downstream, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			log.Println("accept error", err)
			return err
		}

		log := log.New(
			os.Stderr,
			fmt.Sprintf("%s > ", downstream.RemoteAddr()),
			log.LstdFlags|log.Lmsgprefix,
		)

		log.Println("handling")

		upstream, err := net.Dial("unix", upstreamSock)
		if err != nil {
			log.Println("dial error", err)
			return err
		}

		log.Println("dialed", upstream.RemoteAddr())

		wg := new(sync.WaitGroup)

		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(downstream, upstream)
			_ = downstream.Close()
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(upstream, downstream)
			_ = upstream.Close()
		}()

		go func() {
			wg.Wait()
			log.Println("done")
		}()
	}
}
