// Command dump-id decodes a dagql call ID (a "recipe") and reports on it.
//
// The default output is the human-readable idtui dump. The other modes are
// data-oriented, meant for forensics on a saved conversation's recipe:
//
//	-json    the decoded callpbv1.DAG as structured JSON (lossless)
//	-stats   per-field counts, chain depth, expansion (re-execution) counts
//	-tree    the receiver spine, with ID-valued arguments expanded inline
//	-find    every call whose (qualified) name matches a regexp
//	-diff    structural diff against another recipe
//
// Input is a base64 ID on stdin, a file containing one, or a saved session
// JSON file (the `llm_id` field is extracted automatically).
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dagger/dagger/dagql/idtui"
)

func main() {
	var (
		sessionFlag string
		jsonOut     bool
		statsOut    bool
		treeOut     bool
		findPat     string
		diffWith    string
		maxLit      int
		spineDepth  int
		treeDepth   int
	)
	flag.StringVar(&sessionFlag, "session", "", "read the ID from this file; a saved-session JSON file's llm_id is extracted automatically ('-' = stdin)")
	flag.BoolVar(&jsonOut, "json", false, "emit the decoded callpbv1.DAG as structured JSON")
	flag.BoolVar(&statsOut, "stats", false, "emit per-field counts, chain depth and expansion counts")
	flag.BoolVar(&treeOut, "tree", false, "emit the receiver spine with ID-valued arguments expanded inline")
	flag.StringVar(&findPat, "find", "", "emit every call whose Type.field name matches this regexp")
	flag.StringVar(&diffWith, "diff", "", "structurally diff the input recipe against the recipe in this file")
	flag.IntVar(&maxLit, "lit", 72, "truncate literal values to this many characters (0 = no truncation)")
	flag.IntVar(&spineDepth, "spine", 12, "show at most this many trailing receiver selectors in a path (0 = all)")
	flag.IntVar(&treeDepth, "depth", 0, "in -tree, only recurse this many levels into ID arguments (0 = unlimited)")
	flag.Parse()

	input := sessionFlag
	if input == "" && flag.NArg() > 0 {
		input = flag.Arg(0)
	}

	src, err := load(input)
	if err != nil {
		fatal(err)
	}

	fmtOpts := formatOpts{maxLit: maxLit, spine: spineDepth}

	any := false
	out := os.Stdout

	if jsonOut {
		any = true
		b, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(src.dag)
		if err != nil {
			fatal(err)
		}
		out.Write(b)
		fmt.Fprintln(out)
	}

	if statsOut {
		any = true
		src.graph.printStats(out, src, fmtOpts)
	}

	if treeOut {
		any = true
		src.graph.printTree(out, fmtOpts, treeDepth)
	}

	if findPat != "" {
		any = true
		re, err := regexp.Compile(findPat)
		if err != nil {
			fatal(err)
		}
		src.graph.printFind(out, re, fmtOpts)
	}

	if diffWith != "" {
		any = true
		other, err := load(diffWith)
		if err != nil {
			fatal(err)
		}
		printDiff(out, src, other, fmtOpts)
	}

	if !any {
		if err := new(idtui.Dump).DumpID(idtui.NewOutput(out), src.id); err != nil {
			fatal(err)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "dump-id:", err)
	os.Exit(1)
}
