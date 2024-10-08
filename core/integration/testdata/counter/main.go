package main

import (
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
)

func main() {
	var count int32
	http.ListenAndServe(":80", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint: gosec
		res := atomic.AddInt32(&count, 1)
		log.Println("count:", res)
		fmt.Fprint(w, res)
	}))
}
