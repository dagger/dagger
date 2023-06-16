package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dagger/dagger/telemetry"
	"github.com/vito/progrock"

	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2themes/d2themescatalog"
	"oss.terrastruct.com/d2/lib/textmeasure"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <input journal> <output svg>\n", os.Args[0])
		os.Exit(1)
	}
	var (
		input  = os.Args[1]
		output = os.Args[2]
	)
	pl := loadEvents(input)
	graph := generateGraph(pl.Vertices())
	svg, err := renderSVG(graph)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(output, svg, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func loadEvents(journal string) *telemetry.Pipeliner {
	f, err := os.Open(journal)
	if err != nil {
		panic(err)
	}

	defer f.Close()

	pl := telemetry.NewPipeliner()

	dec := json.NewDecoder(f)

	for {
		var update progrock.StatusUpdate
		if err := dec.Decode(&update); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			panic(err)
		}

		if err := pl.WriteStatus(&update); err != nil {
			panic(err)
		}
	}

	return pl
}

func generateGraph(vertices []*telemetry.PipelinedVertex) string {
	s := strings.Builder{}

	vertexToGraphID := map[string]string{}
	for _, v := range vertices {
		w := WrappedVertex{v}

		if w.Internal() {
			continue
		}

		graphPath := []string{}
		for _, p := range w.Pipeline() {
			graphPath = append(graphPath, fmt.Sprintf("%q", p.Name))
		}
		graphPath = append(graphPath, fmt.Sprintf("%q", w.ID()))
		graphID := strings.Join(graphPath, ".")

		duration := w.Duration().Round(time.Second / 10).String()
		if w.Cached() {
			duration = "CACHED"
		}

		// `$` has special meaning in D2
		name := strings.ReplaceAll(w.Name(), "$", "") + " (" + duration + ")"

		vertexToGraphID[w.ID()] = graphID
		s.WriteString(graphID + ": {\n")
		s.WriteString(fmt.Sprintf("  label: %q\n", name))
		s.WriteString("}\n")
	}

	for _, v := range vertices {
		w := WrappedVertex{v}
		if w.Internal() {
			continue
		}

		graphID := vertexToGraphID[w.ID()]
		if graphID == "" {
			fmt.Printf("id %s not found\n", w.ID())
			continue
		}
		for _, input := range w.Inputs() {
			source := vertexToGraphID[input]
			if source == "" {
				continue
			}
			s.WriteString(fmt.Sprintf("%s <- %s\n", graphID, source))
		}
	}

	return s.String()
}

func renderSVG(graph string) ([]byte, error) {
	ruler, err := textmeasure.NewRuler()
	if err != nil {
		return nil, err
	}
	diagram, _, err := d2lib.Compile(context.Background(), graph, &d2lib.CompileOptions{
		Layout: func(ctx context.Context, g *d2graph.Graph) error {
			return d2dagrelayout.Layout(ctx, g, nil)
		},
		Ruler:   ruler,
		ThemeID: d2themescatalog.NeutralDefault.ID,
	})
	if err != nil {
		return nil, err
	}
	return d2svg.Render(diagram, &d2svg.RenderOpts{
		Pad: d2svg.DEFAULT_PADDING,
	})
}
