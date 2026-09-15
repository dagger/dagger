package main

import (
	"io"
	"log"
	"net/http"
)

func main() {
	const bindAddr = ":8080"

	pipe := make(chan []byte)
	http.Handle("/write", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		log.Println("writing to pipe:", string(payload))
		pipe <- payload
		log.Println("wrote to pipe")
	}))

	http.Handle("/read", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println("reading from pipe")
		msg := <-pipe
		w.Write(msg)
		log.Println("read from pipe:", string(msg))
	}))

	log.Println("listening:", bindAddr)
	http.ListenAndServe(bindAddr, nil)
}
