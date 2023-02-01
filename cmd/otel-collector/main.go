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
	cmd.Flags().StringArray("tag", []string{}, "tags")

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
		tagList, err := cmd.Flags().GetStringArray("tag")
		if err != nil {
			return err
		}
		tags, err := parseTags(tagList)
		if err != nil {
			return err
		}

		ch := loadEvents(args[0])
		vertices := mergeVertices(ch)
		trace := NewTraceExporter(name, vertices, tags)

		now := time.Now()
		err = trace.Run(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "=> traces completed in %s\n", time.Since(now))

		now = time.Now()
		if err := logSummary(name, vertices, tags, trace.TraceID()); err != nil {
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
	seenVertices := map[string]*bkclient.Vertex{}
	vertices := []*bkclient.Vertex{}
	for msg := range ch {
		if msg == nil {
			break
		}
		for _, v := range msg.Vertexes {
			if v.Completed == nil {
				continue
			}
			if _, ok := seenVertices[v.Digest.String()]; ok {
				continue
			}

			seenVertices[v.Digest.String()] = v
			vertices = append(vertices, v)
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

func parseTags(tags []string) (map[string]string, error) {
	res := make(map[string]string)
	for _, l := range tags {
		parts := strings.SplitN(l, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed tag: %q", l)
		}
		res[parts[0]] = parts[1]
	}
	return res, nil
}
