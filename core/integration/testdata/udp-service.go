package main

import (
	"log"
	"net"
)

func main() {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{
		IP:   net.IPv4(0, 0, 0, 0),
		Port: 4321,
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("UDP listening on %s", conn.LocalAddr())

	tcpL, err := net.Listen("tcp", ":4322")
	if err != nil {
		log.Fatal(err)
	}
	defer tcpL.Close()

	log.Printf("TCP listening on %s (for health check)", tcpL.Addr())

	go func() {
		for {
			c, err := tcpL.Accept()
			if err != nil {
				break
			}
			c.Close()
		}
	}()

	b := make([]byte, 1024)

	for {
		n, remote, err := conn.ReadFromUDP(b)
		if err != nil {
			panic(err)
		}

		log.Printf("read %d bytes from %s", n, remote)

		n, err = conn.WriteTo(b[0:n], remote)
		if err != nil {
			panic(err)
		}

		log.Printf("echoed %d bytes to %s", n, remote)
	}
}
