package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	bkclient "github.com/moby/buildkit/client"
	"github.com/spf13/cobra"
)

func main() {
	cmd.Flags().String("name", "pipeline", "name")
	cmd.Flags().StringArray("label", []string{}, "labels")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

var cmd = &cobra.Command{
	Use:  "otel-collector <JOURNAL_FILE>",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return err
		}
		labelList, err := cmd.Flags().GetStringArray("label")
		if err != nil {
			return err
		}
		labels, err := parseLabels(labelList)
		if err != nil {
			return err
		}

		ch := loadEvents(args[0])
		vertices := mergeVertices(ch)
		trace := NewTraceExporter(name, vertices, labels)

		now := time.Now()
		err = trace.Run(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "=> traces completed in %s\n", time.Since(now))

		now = time.Now()
		if err := logSummary(name, vertices, labels); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "=> logs completed in %s\n", time.Since(now))

		now = time.Now()
		printSummary(os.Stdout, trace)
		fmt.Fprintf(os.Stderr, "=> summary completed in %s\n", time.Since(now))
		return nil
	},
}

func mergeVertices(ch chan *bkclient.SolveStatus) []Vertex {
	vertexByID := map[string]*bkclient.Vertex{}
	vertices := []*bkclient.Vertex{}
	for msg := range ch {
		if msg == nil {
			break
		}
		for _, v := range msg.Vertexes {
			vertex := vertexByID[v.Digest.String()]
			if vertex == nil {
				vertex = v
				vertexByID[v.Digest.String()] = v
				vertices = append(vertices, v)
			}

			vertex.Name = v.Name
			vertex.Cached = v.Cached

			if vertex.Started == nil && v.Started != nil {
				vertex.Started = v.Started
			}
			if v.Completed != nil {
				if vertex.Completed == nil || vertex.Completed.Before(*v.Completed) {
					vertex.Completed = v.Completed
				}
			}
		}
	}

	list := VertexList{}
	for _, v := range vertices {
		list = append(list, Vertex{v})
	}

	return list
}

func loadEvents(journal string) chan *bkclient.SolveStatus {
	f, err := os.Open(journal)
	if err != nil {
		panic(err)
	}

	s := bufio.NewScanner(f)
	s.Split(bufio.ScanLines)

	decoder := json.NewDecoder(f)

	ch := make(chan *bkclient.SolveStatus)
	go func() {
		defer close(ch)
		for {
			entry := struct {
				Event *bkclient.SolveStatus
				TS    string
			}{}

			err := decoder.Decode(&entry)
			if err == io.EOF {
				break
			}
			if err != nil {
				panic(err)
			}

			ch <- entry.Event
		}
	}()

	return ch
}

func parseLabels(labels []string) (map[string]string, error) {
	res := make(map[string]string)
	for _, l := range labels {
		parts := strings.SplitN(l, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed label: %q", l)
		}
		res[parts[0]] = parts[1]
	}
	return res, nil
}
