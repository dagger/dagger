package schema

import (
	"net/http"
	"sync"
)

// flushAfterNBytes creates a middleware that flushes the response
// after every N bytes.
//
// This is used to support streaming requests over gRPC, which may need
// chunking in order to not exceed the max gRPC message size.
func flushAfterNBytes(n int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w,
					"Streaming unsupported: ResponseWriter does not implement http.Flusher",
					http.StatusInternalServerError)
				return
			}

			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w,
					"Streaming unsupported: ResponseWriter does not implement http.Hijacker",
					http.StatusInternalServerError)
				return
			}

			flushWriter := &flushAfterNBytesWriter{
				ResponseWriter: w,
				Flusher:        flusher,
				Hijacker:       hijacker,
				limit:          n,
				mu:             &sync.Mutex{},
			}
			defer flushWriter.Flush()

			next.ServeHTTP(flushWriter, r)
		})
	}
}

type flushAfterNBytesWriter struct {
	http.ResponseWriter
	http.Flusher
	http.Hijacker

	limit   int
	written int
	mu      *sync.Mutex
}

func (w *flushAfterNBytesWriter) Write(bytes []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.ResponseWriter.Write(bytes)
	if err != nil {
		return n, err
	}

	w.written += n
	if w.written >= w.limit {
		w.Flusher.Flush()
		w.written = 0
	}

	return n, nil
}
