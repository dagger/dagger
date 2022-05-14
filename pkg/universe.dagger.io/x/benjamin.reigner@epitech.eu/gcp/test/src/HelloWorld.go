package helloworld

import (
        "fmt"
        "net/http"
)

// HelloGet is an HTTP Cloud Function.
func HelloWorld(w http.ResponseWriter, r *http.Request) {
        fmt.Fprint(w, "Hello World!")
}
