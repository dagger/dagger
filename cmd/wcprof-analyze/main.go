// wcprof-analyze reads a wcprof dump (from the engine's /debug/wcprof/dump
// endpoint) and reports wall-clock bottleneck analysis: per-class self time,
// counterfactual what-if rankings, blocking chains, and dead air.
//
// Usage:
//
//	curl -s http://localhost:6060/debug/wcprof/dump > /tmp/wcprof.dump
//	go run ./cmd/wcprof-analyze /tmp/wcprof.dump
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/engine/wcprof/wcanalyze"
)

func main() {
	var (
		topClasses = flag.Int("top", 30, "number of classes to show in rankings")
		factorsStr = flag.String("factors", "0,0.5,0.9", "comma-separated self-time scaling factors for what-if simulation")
		minSelf    = flag.Duration("min-self", time.Millisecond, "ignore classes with less total self-time than this in what-ifs")
		deadAirMin = flag.Duration("dead-air-min", 50*time.Millisecond, "minimum gap to report as dead air")
		chainDepth = flag.Int("chain-depth", 25, "max length of the blocking chain to print")
	)
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: wcprof-analyze [flags] <dump-file> [more-dump-files...]\n")
		fmt.Fprintf(os.Stderr, "multiple dumps from periodic drains of the same engine run are merged\n")
		flag.PrintDefaults()
		os.Exit(2)
	}

	var factors []float64
	for _, part := range strings.Split(*factorsStr, ",") {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid factor %q: %v\n", part, err)
			os.Exit(2)
		}
		factors = append(factors, f)
	}

	if err := run(flag.Args(), wcanalyze.ReportOptions{
		TopClasses:     *topClasses,
		WhatIfFactors:  factors,
		MinClassSelfNS: int64(*minSelf),
		DeadAirMinNS:   int64(*deadAirMin),
		ChainDepth:     *chainDepth,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(paths []string, opts wcanalyze.ReportOptions) error {
	readers := make([]io.Reader, 0, len(paths))
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		readers = append(readers, f)
	}

	graph, err := wcanalyze.LoadMulti(readers)
	if err != nil {
		return fmt.Errorf("load dumps: %w", err)
	}
	return wcanalyze.WriteReport(os.Stdout, graph, opts)
}
