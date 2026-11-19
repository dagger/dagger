// heapstat reads a Go heap profile (as served by /debug/pprof/heap) and
// prints, one per line:
//
//	inuse <bytes>              total inuse_space of the profile
//	focus <bytes>              inuse_space of samples with a frame matching -focus
//	top <bytes> <function>     the -top heaviest focus sites
//
// A sample matches -focus when any frame's "<function> <file>" matches the
// regexp, like `go tool pprof -focus`. A matching sample is attributed, for
// the top list, to its leaf-most matching frame: the PR's own code closest to
// the allocation, which is what a memory-cake demo wants to point at.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"

	"github.com/google/pprof/profile"
)

func main() {
	focus := flag.String("focus", "", "regexp over stack frames (\"<function> <file>\"); empty matches nothing")
	top := flag.Int("top", 8, "number of focus sites to print")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: heapstat [-focus RE] [-top N] heap.pb.gz")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *focus, *top); err != nil {
		fmt.Fprintln(os.Stderr, "heapstat:", err)
		os.Exit(1)
	}
}

func run(path, focus string, top int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return err
	}
	idx := -1
	for i, st := range p.SampleType {
		if st.Type == "inuse_space" {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("%s: no inuse_space sample type", path)
	}
	var re *regexp.Regexp
	if focus != "" {
		re, err = regexp.Compile(focus)
		if err != nil {
			return err
		}
	}
	var inuse, focused int64
	sites := map[string]int64{}
	for _, s := range p.Sample {
		v := s.Value[idx]
		inuse += v
		if re == nil {
			continue
		}
		// Locations are leaf first; within a location, inlined lines are
		// leaf first too.
		site := ""
	frames:
		for _, loc := range s.Location {
			for _, ln := range loc.Line {
				if ln.Function == nil {
					continue
				}
				if re.MatchString(ln.Function.Name + " " + ln.Function.Filename) {
					site = ln.Function.Name
					break frames
				}
			}
		}
		if site != "" {
			focused += v
			sites[site] += v
		}
	}
	fmt.Printf("inuse %d\n", inuse)
	fmt.Printf("focus %d\n", focused)
	names := make([]string, 0, len(sites))
	for n := range sites {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return sites[names[i]] > sites[names[j]] })
	for i, n := range names {
		if i >= top {
			break
		}
		fmt.Printf("top %d %s\n", sites[n], n)
	}
	return nil
}
