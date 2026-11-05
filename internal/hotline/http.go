package hotline

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

func mockHandler(store *mockState, p Principal) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hotline/threads", func(w http.ResponseWriter, r *http.Request) {
		threads, err := store.List(r.Context(), p)
		respond(w, threads, err)
	})
	mux.HandleFunc("POST /v1/hotline/threads", func(w http.ResponseWriter, r *http.Request) {
		var in Input
		if err := decode(w, r, &in); err != nil {
			writeError(w, err)
			return
		}
		sent, err := store.Send(r.Context(), p, 0, r.Header.Get("Idempotency-Key"), in)
		respond(w, sent, err)
	})
	mux.HandleFunc("GET /v1/hotline/threads/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := resourceID(r)
		if err != nil {
			writeError(w, err)
			return
		}
		var after int64
		if value := r.URL.Query().Get("after"); value != "" {
			after, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				writeError(w, problem(400, "invalid_input", "Use a numeric message ID for after."))
				return
			}
		}
		conversation, err := store.Read(r.Context(), p, id, after)
		respond(w, conversation, err)
	})
	mux.HandleFunc("POST /v1/hotline/threads/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		id, err := resourceID(r)
		if err != nil {
			writeError(w, err)
			return
		}
		var in Input
		if err := decode(w, r, &in); err != nil {
			writeError(w, err)
			return
		}
		sent, err := store.Send(r.Context(), p, id, r.Header.Get("Idempotency-Key"), in)
		respond(w, sent, err)
	})
	mux.HandleFunc("GET /v1/hotline/messages/{id}/attachments", func(w http.ResponseWriter, r *http.Request) {
		id, err := resourceID(r)
		if err != nil {
			writeError(w, err)
			return
		}
		bundle, err := store.Bundle(r.Context(), p, id)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="workspace.zip"`)
		w.Write(bundle)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func resourceID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, problem(400, "invalid_input", "Use a positive ID.")
	}
	return id, nil
}

func decode(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 3*MaxAttachmentBytes)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return problem(400, "invalid_input", "Use a valid JSON body with known fields.")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return problem(400, "invalid_input", "Supply one JSON object.")
	}
	return nil
}

func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = problem(503, "unavailable", "The service is unavailable. Retry with the same request ID.")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	json.NewEncoder(w).Encode(map[string]any{"error": e})
}
