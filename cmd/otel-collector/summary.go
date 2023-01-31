package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	traceURL   = "https://daggerboard.grafana.net/explore?orgId=1&left=%7B%22datasource%22:%22grafanacloud-traces%22,%22queries%22:%5B%7B%22refId%22:%22A%22,%22datasource%22:%7B%22type%22:%22tempo%22,%22uid%22:%22grafanacloud-traces%22%7D,%22queryType%22:%22traceId%22,%22query%22:%22{TRACE_ID}%22%7D%5D,%22range%22:%7B%22from%22:%22now-1h%22,%22to%22:%22now%22%7D%7D"
	metricsURL = "https://daggerboard.grafana.net/d/SyaItlTVk/dagger-overview?from={FROM}&to={TO}&var-detail=pipeline&var-micros=1000000"
)

func printSummary(w io.Writer, exporter *TraceExporter) {
	vertices := exporter.Vertices()
	duration := vertices.Duration().Round(time.Second / 10).String()

	fmt.Fprintf(w, "🚀 Dagger pipeline completed in **%s**\n\n", duration)

	printBreakdown(w, exporter.Vertices())

	traceRunURL := strings.ReplaceAll(traceURL, "{TRACE_ID}", exporter.TraceID())
	metricsRunURL := strings.ReplaceAll(metricsURL, "{FROM}", strconv.FormatInt(vertices.Started().UnixMilli(), 10))
	metricsRunURL = strings.ReplaceAll(metricsRunURL, "{TO}", strconv.FormatInt(vertices.Completed().UnixMilli(), 10))
	fmt.Fprintf(w, "\n- 📈 [Explore metrics](%s)\n", metricsRunURL)
	fmt.Fprintf(w, "\n- 🔍 [Explore traces](%s)\n", traceRunURL)

	fmt.Fprintf(w, "\n\n### DAG\n")
	fmt.Fprintf(w, "```mermaid\n")
	fmt.Fprint(w, printGraph(exporter.Vertices()))
	fmt.Fprintf(w, "```\n")
}

func printBreakdown(w io.Writer, vertices VertexList) {
	tw := tabwriter.NewWriter(w, 4, 4, 1, ' ', 0)
	defer tw.Flush()

	pipelines := vertices.ByPipeline()
	pipelineNames := []string{}
	for p := range pipelines {
		pipelineNames = append(pipelineNames, p)
	}
	sort.Strings(pipelineNames)

	fmt.Fprintf(tw, "| **Pipeline** \t| **Duration** \t|\n")
	fmt.Fprintf(tw, "| --- \t| --- \t|\n")
	for _, pipeline := range pipelineNames {
		vertices := pipelines[pipeline]
		status := "✅"
		if vertices.Error() != nil {
			status = "❌"
		}
		duration := vertices.Duration().Round(time.Second / 10).String()
		if vertices.Cached() {
			duration = "CACHED"
		}

		fmt.Fprintf(tw, "| %s **%s** \t| %s \t|\n", status, pipeline, duration)
	}
}

func printGraph(vertices VertexList) string {
	s := strings.Builder{}
	s.WriteString("flowchart TD\n")

	for _, v := range vertices {
		duration := v.Duration().Round(time.Second / 10).String()
		if v.Cached() {
			duration = "CACHED"
		}
		name := strings.ReplaceAll(v.Name(), "\"", "") + " (" + duration + ")"
		s.WriteString(fmt.Sprintf("  %s[%q]\n", v.ID(), name))
	}

	for _, v := range vertices {
		for _, input := range v.Inputs() {
			s.WriteString(fmt.Sprintf("  %s --> %s\n", input, v.ID()))
		}
	}

	return s.String()
}
