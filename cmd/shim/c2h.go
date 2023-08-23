package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

func tunnel(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tunnel <socket>:<port>[/<tcp|udp>] [...]")
	}

	eg, ctx := errgroup.WithContext(context.Background())

	for _, trio := range args {
		upstreamSock, portSpec, ok := strings.Cut(trio, ":")
		if !ok {
			return fmt.Errorf("invalid socket:port:protocol spec: %s", trio)
		}

		port, network, ok := strings.Cut(portSpec, "/")
		if !ok {
			network = "tcp"
		}

		eg.Go(func() error {
			return tunnelOne(ctx, upstreamSock, port, network)
		})
	}

	return eg.Wait()
}

func tunnelOne(ctx context.Context, upstreamSock, port, network string) error {
	log.Printf("listening on %s/%s", port, network)

	l, err := net.Listen(network, fmt.Sprintf(":%s", port))
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	for {
		downstream, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			log.Println("fatal accept error:", err)
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
			log.Println("dial error:", err)
			_ = downstream.Close()
			continue
		}

		log.Println("dialed", upstream.RemoteAddr())

		wg := new(sync.WaitGroup)

		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := io.Copy(downstream, upstream)
			if err != nil && !errors.Is(err, io.EOF) {
				log.Println("copy upstream->downstream error", err)
			}
			_ = downstream.Close()
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := io.Copy(upstream, downstream)
			if err != nil && !errors.Is(err, io.EOF) {
				log.Println("copy downstream->upstream error", err)
			}
			_ = upstream.Close()
		}()

		go func() {
			wg.Wait()
			log.Println("done")
		}()
	}
}
