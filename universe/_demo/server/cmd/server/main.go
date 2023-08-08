package main

import (
	"fmt"
	"net/http"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		respMsg := fmt.Sprintf("Hello, %s!", r.RemoteAddr)
		fmt.Fprintln(w, respMsg)
		w.WriteHeader(http.StatusOK)
	})

	httpSrv := http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}
	defer httpSrv.Close()
	httpSrv.ListenAndServe()
}

func getMsg(sayHelloTo string) string {
	return fmt.Sprintf("Hello, %s!", sayHelloTo)
}
