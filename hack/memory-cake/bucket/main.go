// Command bucket buckets a heap profile into the memory cake's layers (see
// hack/memory-cake/layers), to check or tune the rules on a kept profile:
//
//	go run ./hack/memory-cake/bucket heap.pb.gz              # layer totals
//	go run ./hack/memory-cake/bucket -stacks other heap.pb.gz # a layer's top stacks
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/pprof/profile"

	"github.com/dagger/dagger/hack/memory-cake/layers"
)

func main() {
	stacks := flag.String("stacks", "", "print the top stacks of this layer")
	n := flag.Int("n", 15, "how many sites or stacks to print")
	depth := flag.Int("depth", 25, "frames per stack")
	asJSON := flag.Bool("json", false, "print the bucketed profile as JSON")
	match := flag.String("match", "", "with -stacks, only stacks with a frame matching this regexp")
	flag.Parse()
	matchRe := regexp.MustCompile(*match)
	f, err := os.Open(flag.Arg(0))
	check(err)
	p, err := profile.Parse(f)
	check(err)
	res, err := layers.Bucket(p, *n)
	check(err)
	if *asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return
	}
	if *stacks == "" {
		for _, l := range res.Layers {
			fmt.Printf("%-12s %8.1f MiB\n", l.ID, float64(l.Bytes)/(1<<20))
		}
		fmt.Printf("%-12s %8.1f MiB\n", "total", float64(res.TotalBytes)/(1<<20))
		return
	}
	idx := -1
	for i, st := range p.SampleType {
		if st.Type == "inuse_space" {
			idx = i
		}
	}
	type stack struct {
		frames []string
		bytes  int64
	}
	agg := map[string]*stack{}
	for _, s := range p.Sample {
		var frames []string
		for _, loc := range s.Location {
			for _, line := range loc.Line {
				if line.Function != nil {
					frames = append(frames, line.Function.Name)
				}
			}
		}
		if layers.Classify(frames) != *stacks {
			continue
		}
		if *match != "" && !slices.ContainsFunc(frames, matchRe.MatchString) {
			continue
		}
		// Collapse recursion: a run of one frame prints once.
		frames = slices.Compact(frames)
		if len(frames) > *depth {
			frames = frames[:*depth]
		}
		key := strings.Join(frames, "\n")
		if agg[key] == nil {
			agg[key] = &stack{frames: frames}
		}
		agg[key].bytes += s.Value[idx]
	}
	var all []*stack
	for _, s := range agg {
		all = append(all, s)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].bytes > all[j].bytes })
	for i, s := range all {
		if i >= *n {
			break
		}
		fmt.Printf("---- %.1f MiB\n", float64(s.bytes)/(1<<20))
		for _, f := range s.frames {
			fmt.Println("  " + f)
		}
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "bucket:", err)
		os.Exit(1)
	}
}
