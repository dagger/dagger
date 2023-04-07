package main

import (
	"io"
	"unicode/utf8"
)

type UTF8DanglingWriter struct {
	dangling []byte
	w        io.Writer
}

func NewUTF8DanglingWriter(w io.Writer) *UTF8DanglingWriter {
	return &UTF8DanglingWriter{
		w: w,
	}
}

func (w *UTF8DanglingWriter) Write(b []byte) (int, error) {
	data := w.writeDangling(b)
	_, err := w.w.Write(data)
	return len(b), err
}

func (w *UTF8DanglingWriter) writeDangling(b []byte) []byte {
	data := append(w.dangling, b...)

	checkEncoding, _ := utf8.DecodeLastRune(data)
	if checkEncoding == utf8.RuneError {
		w.dangling = data
		return nil
	}

	w.dangling = nil
	return data
}
