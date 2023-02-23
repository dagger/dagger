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

	log.Printf("listening on %s", conn.LocalAddr())

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
