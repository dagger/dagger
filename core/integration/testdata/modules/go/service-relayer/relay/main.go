package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
)

func main() {
	http.ListenAndServe(":80", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint: gosec
		log.Printf("HANDLE: %+v", r.URL)

		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		relay := q["relay"]
		end := q.Get("end")
		if len(relay) == 0 {
			log.Println("reached end:", end)
			fmt.Fprint(w, end)
			return
		}

		first, rest := relay[0], relay[1:]

		next, err := url.Parse(first)
		if err != nil {
			log.Println("PARSE ERROR:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		next.RawQuery = url.Values{
			"relay": rest,
			"end":   {end},
		}.Encode()

		log.Println("relaying:", next)

		cat := exec.Command("cat", "/etc/resolv.conf")
		cat.Stdout = os.Stdout
		cat.Stderr = os.Stderr
		cat.Run()

		res, err := http.Get(next.String())
		if err != nil {
			log.Println("GET ERROR:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer res.Body.Close()

		body, err := io.ReadAll(res.Body)
		if err != nil {
			log.Println("READ ERROR:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "%s: %s", first, string(body))
	}))
}
