package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		respMsg, err := getMsg(r.RemoteAddr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, respMsg)
	})

	httpSrv := http.Server{
		Addr:              ":8081",
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}
	defer httpSrv.Close()
	fmt.Println("Listening on :8081")
	httpSrv.ListenAndServe()
}

func getMsg(remoteAddr string) (string, error) {
	ip, port, ok := strings.Cut(remoteAddr, ":")
	if !ok {
		return "", fmt.Errorf("invalid remote address: %s", remoteAddr)
	}

	return fmt.Sprintf("Hello, %s form port %s!", ip, port), nil // BUG
}
