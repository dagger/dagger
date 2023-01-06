package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	bkclient "github.com/moby/buildkit/client"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otrace "go.opentelemetry.io/otel/trace"

	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

const (
	tracer        = "dagger"
	rootContextID = ""
	rootSpanName  = "pipeline"
)

type OtelCollector struct {
	tr otrace.Tracer

	rootSpan        otrace.Span
	vertexByID      map[string]*bkclient.Vertex
	contextByVertex map[string]context.Context
	contextByGroup  map[string]context.Context
}

func NewCollector() *OtelCollector {
	return &OtelCollector{
		vertexByID:      make(map[string]*bkclient.Vertex),
		contextByVertex: make(map[string]context.Context),
		contextByGroup:  make(map[string]context.Context),
	}
}

func vertexGroup(v *bkclient.Vertex) []string {
	group := []string{}
	if groupID := v.ProgressGroup.GetId(); groupID != "" {
		if err := json.Unmarshal([]byte(groupID), &group); err != nil {
			panic(err)
		}
	}
	return group
}

func (c *OtelCollector) Run(ctx context.Context, ch chan *bkclient.SolveStatus) error {
	exp, err := newExporter()
	if err != nil {
		return err
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(newResource()),
	)
	defer func() {
		_ = tp.Shutdown(context.Background())
	}()
	otel.SetTracerProvider(tp)

	c.tr = otel.Tracer(tracer)

	var rootCtx context.Context
	wg := sync.WaitGroup{}
	for ev := range ch {
		for ev == nil {
			break
		}

		for _, v := range ev.Vertexes {
			// On very first event, start root span
			if c.rootSpan == nil {
				rootCtx, c.rootSpan = c.tr.Start(ctx,
					rootSpanName,
					otrace.WithTimestamp(*v.Started),
				)
				c.contextByGroup[rootContextID] = rootCtx
			}

			// Ignore vertex that haven't started yet
			if v.Started == nil {
				continue
			}

			id := v.Digest.String()
			last := c.vertexByID[id]
			if last == nil {
				c.startSpan(v)
				c.vertexByID[id] = v
				last = v
			}
			vCtx := c.contextByVertex[id]
			span := otrace.SpanFromContext(vCtx)
			if v.Cached {
				span.SetAttributes(attribute.Bool("cached", v.Cached))
			}
			if v.Completed != nil && last.Completed == nil {
				// buildkit might decide later on this vertex was not completed.
				// So we need to debounce -- wait 100ms, if we didn't receive any updates,
				// then it's really completed.
				v := v
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						time.Sleep(100 * time.Millisecond)
						current := c.vertexByID[v.Digest.String()]
						if v != current {
							v = current
							continue
						}
						span.End(otrace.WithTimestamp(*v.Completed))
						break
					}
				}()
			}
			c.vertexByID[id] = v
			fmt.Fprintf(os.Stderr, "%s => %s\n", span.SpanContext().TraceID(), span.SpanContext().SpanID())
		}
	}

	wg.Wait()

	c.endGroups()

	return nil
}

func (c *OtelCollector) groupContext(v *bkclient.Vertex) context.Context {
	group := vertexGroup(v)

	ctx := c.contextByGroup[rootContextID]
	for i := range group {
		parent := strings.Join(group[0:i+1], "/")
		parentCtx := c.contextByGroup[parent]
		if parentCtx == nil {
			parentCtx, _ = c.tr.Start(ctx, group[i], otrace.WithTimestamp(*v.Started))
			c.contextByGroup[parent] = parentCtx
		}
		ctx = parentCtx
	}

	return ctx
}

func (c *OtelCollector) startSpan(v *bkclient.Vertex) {
	id := v.Digest.String()
	c.vertexByID[id] = v

	// Register links for vertex inputs
	links := []otrace.Link{}
	for _, input := range v.Inputs {
		inputCtx := c.contextByVertex[input.String()]
		if inputCtx == nil {
			fmt.Printf("input %s not found\n", input.String())
			continue
		}
		inputLink := otrace.LinkFromContext(inputCtx)
		links = append(links, inputLink)
	}

	groupCtx := c.groupContext(v)
	vertexCtx, _ := c.tr.Start(
		groupCtx,
		v.Name,
		otrace.WithTimestamp(*v.Started),
		otrace.WithLinks(links...),
	)
	c.contextByVertex[id] = vertexCtx
}

func (c *OtelCollector) endGroups() {
	// End all groups
	for groupName, ctx := range c.contextByGroup {
		group := strings.Split(groupName, "/")
		if groupName == rootContextID {
			group = []string{}
		}

		// Find the last completed vertex within the group and use that as group completion time
		vertices := c.verticesForGroup(group)
		fmt.Fprintf(os.Stderr, "group: %+v || vertices: %d\n", group, len(vertices))
		var last *time.Time
		for _, v := range vertices {
			if last == nil || last.Before(*v.Completed) {
				last = v.Completed
			}
		}

		otrace.SpanFromContext(ctx).End(
			otrace.WithTimestamp(*last),
		)
	}
}

func (c *OtelCollector) verticesForGroup(selector []string) []*bkclient.Vertex {
	matches := []*bkclient.Vertex{}
	for _, v := range c.vertexByID {
		if matchGroup(v, selector) {
			matches = append(matches, v)
		}
	}
	return matches
}

func matchGroup(v *bkclient.Vertex, selector []string) bool {
	group := vertexGroup(v)
	if len(selector) > len(group) {
		return false
	}
	for i, sel := range selector {
		if group[i] != sel {
			return false
		}
	}

	return true
}

func (c *OtelCollector) TraceID() string {
	if c.rootSpan == nil {
		return ""
	}
	return c.rootSpan.SpanContext().TraceID().String()
}

func (c *OtelCollector) Duration() time.Duration {
	var (
		first *time.Time
		last  *time.Time
	)
	for _, v := range c.vertexByID {
		if first == nil || first.After(*v.Started) {
			first = v.Started
		}
		if last == nil || last.Before(*v.Completed) {
			last = v.Completed
		}
	}

	return last.Sub(*first)
}

func (c *OtelCollector) Breakdown() map[string]time.Duration {
	breakdown := map[string]time.Duration{}
	for groupName := range c.contextByGroup {
		group := strings.Split(groupName, "/")
		if groupName == rootContextID {
			continue
		}

		vertices := c.verticesForGroup(group)
		var (
			first *time.Time
			last  *time.Time
		)
		for _, v := range vertices {
			if first == nil || first.After(*v.Started) {
				first = v.Started
			}
			if last == nil || last.Before(*v.Completed) {
				last = v.Completed
			}
		}

		breakdown[groupName] = last.Sub(*first)
	}

	return breakdown
}

func (c *OtelCollector) Vertices() map[string]*bkclient.Vertex {
	return c.vertexByID
}

func newExporter() (trace.SpanExporter, error) {
	return otlptracegrpc.New(context.Background())
}

func newResource() *resource.Resource {
	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("dagger"),
			semconv.ServiceVersionKey.String("v0.1.0"),
			attribute.String("environment", "test"),
		),
	)
	if err != nil {
		panic(err)
	}
	return r
}
