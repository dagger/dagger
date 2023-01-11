package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	bkclient "github.com/moby/buildkit/client"

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
	ch := loadEvents(input)
	vertices := mergeVertices(ch)
	graph := generateGraph(vertices)
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
				TS    string
			}{}

			if err := json.Unmarshal(s.Bytes(), &entry); err != nil {
				panic(err)
			}

			ch <- entry.Event
		}
	}()

	return ch
}

func mergeVertices(ch chan *bkclient.SolveStatus) []*bkclient.Vertex {
	vertexByID := map[string]*bkclient.Vertex{}
	vertices := []*bkclient.Vertex{}
	for msg := range ch {
		if msg == nil {
			return vertices
		}
		for _, v := range msg.Vertexes {
			vertex := vertexByID[v.Digest.String()]
			if vertex == nil {
				vertex = v
				vertexByID[v.Digest.String()] = v
				vertices = append(vertices, v)
			}
			if vertex.Started == nil && v.Started != nil {
				vertex.Started = v.Started
			}
			vertex.Name = v.Name
			vertex.Completed = v.Completed
			vertex.Cached = v.Cached
		}
	}

	return vertices
}

func generateGraph(vertices []*bkclient.Vertex) string {
	s := strings.Builder{}

	vertexToGraphID := map[string]string{}
	for _, v := range vertices {
		w := WrappedVertex{v}

		fmt.Fprintf(os.Stderr, "%s => %s [%+v]\n", w.ID(), w.Name(), w.Pipeline())
		if strings.Contains(w.v.Name, "resolve image config for") {
			fmt.Fprintf(os.Stderr, "%q\n", w.v.Name)
		}

		if w.Internal() {
			continue
		}

		graphPath := []string{}
		for _, p := range w.Pipeline() {
			graphPath = append(graphPath, fmt.Sprintf("%q", p.Name))
		}
		graphPath = append(graphPath, fmt.Sprintf("%q", w.Name()))
		graphID := strings.Join(graphPath, ".")
		vertexToGraphID[w.ID()] = graphID
		s.WriteString(fmt.Sprintf("%s: %s\n", graphID, w.Name()))
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
