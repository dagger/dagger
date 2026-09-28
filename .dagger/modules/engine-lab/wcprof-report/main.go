// Command wcprof-report turns an engine wcprof dump (see engine/wcprof) into
// small, agent-sized reports: per-class self time, per-parent child
// breakdowns grouped into shapes, per-client activity over time, one op's
// subtree or direct-children table, or name-resolved NDJSON events for jq.
//
// It lives in the engine-lab module rather than the repo (the offline
// analyzer moved out of tree in #13588); engine-lab builds it at tool-call
// time against the workspace's engine/wcprof package, so it always reads the
// current dump format.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	var (
		opts         Options
		class        string
		excludeClass string
	)
	flag.StringVar(&opts.View, "view", "summary", "report view: "+strings.Join(Views, ", "))
	flag.StringVar(&class, "class", "", "regexp over op classes (e.g. '^Query\\.node$')")
	flag.StringVar(&excludeClass, "exclude-class", "", "regexp over op classes to leave out (e.g. '^Query\\.')")
	flag.StringVar(&opts.Filter.Client, "client", "", "substring of the client ID")
	flag.StringVar(&opts.Filter.Kind, "kind", "", "exact op kind (call, call_exec, lazy, exec, ...)")
	flag.Uint64Var(&opts.Op, "op", 0, "tree/children views: root op ID (default: slowest matching op)")
	flag.IntVar(&opts.Depth, "depth", 6, "tree view: levels to expand below the root")
	flag.IntVar(&opts.Top, "top", 30, "rows per ranking")
	flag.StringVar(&opts.Sort, "sort", "", "classes view: self (default), count or dur; children view: start (default), dur or self")
	flag.IntVar(&opts.Buckets, "buckets", 10, "clients view: number of time buckets")
	flag.IntVar(&opts.Collapse, "collapse", 4, "tree view: aggregate same-class siblings from this many")
	flag.IntVar(&opts.Limit, "limit", 0, "maximum output lines (0 = unlimited; not applied to events)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: wcprof-report [flags] <dump>\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if class != "" {
		re, err := regexp.Compile(class)
		if err != nil {
			fatal(fmt.Errorf("bad -class: %w", err))
		}
		opts.Filter.Class = re
	}
	if excludeClass != "" {
		re, err := regexp.Compile(excludeClass)
		if err != nil {
			fatal(fmt.Errorf("bad -exclude-class: %w", err))
		}
		opts.Filter.ExcludeClass = re
	}

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fatal(err)
	}
	g, err := Load(bufio.NewReaderSize(f, 1<<20))
	f.Close()
	if err != nil {
		fatal(err)
	}
	w := bufio.NewWriter(os.Stdout)
	if err := Report(w, g, opts); err != nil {
		w.Flush()
		fatal(err)
	}
	if err := w.Flush(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "wcprof-report:", err)
	os.Exit(1)
}
