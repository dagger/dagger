package main

import (
	"dagger.io/os"
)

// Repro panic in dagger up

ReproPanic: f: os.#File & {
	// Uncomment this line to fix the panic:
	// from: { #up: [{"do":"fetch-container", "ref":"alpine"}]}
	path: "/does-not-matter.txt"
}
