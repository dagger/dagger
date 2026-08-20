package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"dagger.io/dagger"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	first, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		fatalf("connect first peer: %v", err)
	}
	second, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		_ = first.Close()
		fatalf("connect second peer: %v", err)
	}

	firstVersion, err := first.Version(ctx)
	if err != nil {
		fatalf("query first peer: %v", err)
	}
	secondVersion, err := second.Version(ctx)
	if err != nil {
		fatalf("query second peer: %v", err)
	}
	if firstVersion == "" || firstVersion != secondVersion {
		fatalf("unexpected peer versions: first=%q second=%q", firstVersion, secondVersion)
	}

	if err := first.Close(); err != nil {
		fatalf("close first peer: %v", err)
	}
	if _, err := second.Version(ctx); err != nil {
		fatalf("query second peer after first closed: %v", err)
	}
	if err := second.Close(); err != nil {
		fatalf("close second peer: %v", err)
	}

	fmt.Println("independent Go SDK peer sessions ok")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
