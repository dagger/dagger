// heapstat reads a Go heap profile (as served by /debug/pprof/heap) and
// prints, one per line:
//
//	inuse <bytes>              total inuse_space of the profile
//	focus <bytes>              inuse_space of samples with a frame matching -focus
//	top <bytes> <function>     the -top heaviest focus sites
//	alloc <bytes>              total alloc_space (allocated since the process started)
//	focusalloc <bytes>         alloc_space of samples matching -focus-alloc
//	topalloc <bytes> <function>  the -top heaviest -focus-alloc sites
//
// A sample matches a focus regexp when any frame's "<function> <file>"
// matches it, like `go tool pprof -focus`. A matching sample is attributed,
// for the top lists, to its leaf-most matching frame: the PR's own code
// closest to the allocation, which is what a memory-cake demo wants to point
// at. -focus-alloc defaults to -focus.
//
// A focus can also be clauses separated by " || ", each of terms separated
// by " && ": a sample matches when, for some clause, every term matches one
// of its frames, e.g. "consumeString && exportTraces || SpanAttributes" for
// strings decoded on the trace path, plus anything under SpanAttributes. It
// is attributed to the leaf-most frame matching the clause's LAST term.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
)

func main() {
	focus := flag.String("focus", "", "regexp over stack frames (\"<function> <file>\"); empty matches nothing")
	focusAlloc := flag.String("focus-alloc", "", "regexp for the alloc_space focus (default: -focus)")
	top := flag.Int("top", 8, "number of focus sites to print")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: heapstat [-focus RE] [-focus-alloc RE] [-top N] heap.pb.gz")
		os.Exit(2)
	}
	if *focusAlloc == "" {
		*focusAlloc = *focus
	}
	if err := run(flag.Arg(0), *focus, *focusAlloc, *top); err != nil {
		fmt.Fprintln(os.Stderr, "heapstat:", err)
		os.Exit(1)
	}
}

func run(path, focus, focusAlloc string, top int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return err
	}
	if err := report(p, "inuse_space", focus, "inuse", "focus", "top", top); err != nil {
		return err
	}
	return report(p, "alloc_space", focusAlloc, "alloc", "focusalloc", "topalloc", top)
}

func report(p *profile.Profile, sampleType, focus, totalKey, focusKey, topKey string, top int) error {
	idx := -1
	for i, st := range p.SampleType {
		if st.Type == sampleType {
			idx = i
		}
	}
	if idx < 0 {
		if sampleType == "inuse_space" {
			return fmt.Errorf("no %s sample type", sampleType)
		}
		return nil
	}
	clauses, err := parseFocus(focus)
	if err != nil {
		return err
	}
	var total, focused int64
	sites := map[string]int64{}
	for _, s := range p.Sample {
		v := s.Value[idx]
		total += v
		site := ""
		for _, terms := range clauses {
			if site = matchClause(s, terms); site != "" {
				break
			}
		}
		if site != "" {
			focused += v
			sites[site] += v
		}
	}
	fmt.Printf("%s %d\n", totalKey, total)
	fmt.Printf("%s %d\n", focusKey, focused)
	names := make([]string, 0, len(sites))
	for n := range sites {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return sites[names[i]] > sites[names[j]] })
	for i, n := range names {
		if i >= top {
			break
		}
		fmt.Printf("%s %d %s\n", topKey, sites[n], n)
	}
	return nil
}

// parseFocus splits a focus into clauses of terms (see the package docs).
func parseFocus(focus string) ([][]*regexp.Regexp, error) {
	if focus == "" {
		return nil, nil
	}
	var clauses [][]*regexp.Regexp
	for _, clause := range strings.Split(focus, " || ") {
		var terms []*regexp.Regexp
		for _, term := range strings.Split(clause, " && ") {
			re, err := regexp.Compile(strings.TrimSpace(term))
			if err != nil {
				return nil, err
			}
			terms = append(terms, re)
		}
		clauses = append(clauses, terms)
	}
	return clauses, nil
}

// matchClause returns the site a sample is attributed to if every term
// matches one of its frames: the leaf-most frame matching the last term. ""
// if it doesn't match.
func matchClause(s *profile.Sample, terms []*regexp.Regexp) string {
	site := ""
	for i, re := range terms {
		f := leafMost(s, re)
		if f == "" {
			return ""
		}
		if i == len(terms)-1 {
			site = f
		}
	}
	return site
}

// leafMost returns the name of the sample's leaf-most frame matching re.
// Locations are leaf first; within a location, inlined lines are leaf first
// too.
func leafMost(s *profile.Sample, re *regexp.Regexp) string {
	for _, loc := range s.Location {
		for _, ln := range loc.Line {
			if ln.Function == nil {
				continue
			}
			if re.MatchString(ln.Function.Name + " " + ln.Function.Filename) {
				return ln.Function.Name
			}
		}
	}
	return ""
}
