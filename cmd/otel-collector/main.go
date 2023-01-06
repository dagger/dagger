package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	bkclient "github.com/moby/buildkit/client"
)

const (
	traceURL   = "https://daggerboard.grafana.net/explore?orgId=1&left=%7B%22datasource%22:%22grafanacloud-traces%22,%22queries%22:%5B%7B%22refId%22:%22A%22,%22datasource%22:%7B%22type%22:%22tempo%22,%22uid%22:%22grafanacloud-traces%22%7D,%22queryType%22:%22traceId%22,%22query%22:%22{TRACE_ID}%22%7D%5D,%22range%22:%7B%22from%22:%22now-1h%22,%22to%22:%22now%22%7D%7D"
	metricsURL = "https://daggerboard.grafana.net/d/6uNHQk2Vz/daggerboard?from=now-1h&to=now"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <JOURNAL_FILE>\n", os.Args[0])
		os.Exit(1)
	}
	ch := loadEvents(os.Args[1])
	collector := NewCollector()
	err := collector.Run(context.Background(), ch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// ⚠️ TODO: refactor logSummary & printSummary before merging
	logSummary(collector)
	printSummary(os.Stdout, collector)
}

func printSummary(w io.Writer, collector *OtelCollector) {
	duration := collector.Duration()
	breakdown := collector.Breakdown()
	groups := []string{}
	for g := range breakdown {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	fmt.Fprintf(w, "🚀 Dagger pipeline completed in **%s**\n\n", formatDuration(duration))
	tw := tabwriter.NewWriter(w, 4, 4, 1, ' ', 0)
	fmt.Fprintf(tw, "| **Pipeline** \t| **Duration** \t|\n")
	fmt.Fprintf(tw, "| --- \t| --- \t|\n")
	for _, g := range groups {
		duration := breakdown[g]
		fmt.Fprintf(tw, "| ✅ **%s** \t| %s \t|\n", strings.Join(strings.Split(g, "/"), " / "), formatDuration(duration))
	}
	tw.Flush()

	tracesURL := strings.ReplaceAll(traceURL, "{TRACE_ID}", collector.TraceID())
	fmt.Fprintf(w, "\n- 📈 [Explore metrics](%s)\n", metricsURL)
	fmt.Fprintf(w, "\n- 🔍 [Explore traces](%s)\n", tracesURL)
}

func formatDuration(dur time.Duration) string {
	prec := 1
	sec := dur.Seconds()
	if sec < 10 {
		prec = 2
	} else if sec < 100 {
		prec = 1
	}
	return fmt.Sprintf("%.[2]*[1]fs", sec, prec)
}

func loadEvents(journal string) chan *bkclient.SolveStatus {
	f, err := os.Open(journal)
	if err != nil {
		panic(err)
	}

	s := bufio.NewScanner(f)
	s.Split(bufio.ScanLines)

	ch := make(chan *bkclient.SolveStatus)
	go func() {
		defer close(ch)
		for s.Scan() {
			entry := struct {
				Event *bkclient.SolveStatus
				TS    int64
			}{}

			if err := json.Unmarshal(s.Bytes(), &entry); err != nil {
				panic(err)
			}

			ch <- entry.Event
		}
	}()

	return ch
}
