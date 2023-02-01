package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/dagger/core"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otrace "go.opentelemetry.io/otel/trace"

	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

const (
	tracer = "dagger"
)

type TraceExporter struct {
	name     string
	vertices VertexList
	tags     map[string]string

	tr otrace.Tracer

	rootSpan otrace.Span
	rootCtx  context.Context

	contextByVertex   map[string]context.Context
	contextByPipeline map[string]context.Context
}

func NewTraceExporter(name string, vertices VertexList, tags map[string]string) *TraceExporter {
	return &TraceExporter{
		name:     name,
		vertices: vertices,
		tags:     tags,

		contextByVertex:   make(map[string]context.Context),
		contextByPipeline: make(map[string]context.Context),
	}
}

func (c *TraceExporter) Vertices() VertexList {
	return c.vertices
}

func (c *TraceExporter) Run(ctx context.Context) error {
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

	c.rootCtx, c.rootSpan = c.tr.Start(ctx,
		c.name,
		otrace.WithTimestamp(c.vertices.Started()),
	)
	c.rootSpan.SetAttributes(c.attributes()...)
	c.rootSpan.End(otrace.WithTimestamp(c.vertices.Completed()))

	for _, v := range c.vertices {
		c.sendVertex(v)
	}

	return nil
}

func (c *TraceExporter) sendVertex(v Vertex) {
	// Register links for vertex inputs
	links := []otrace.Link{}
	for _, input := range v.Inputs() {
		inputCtx := c.contextByVertex[input]
		if inputCtx == nil {
			fmt.Fprintf(os.Stderr, "input %s not found\n", input)
			continue
		}
		inputLink := otrace.LinkFromContext(inputCtx)
		links = append(links, inputLink)
	}

	pipelineCtx := c.pipelineContext(v)
	vertexCtx, vertexSpan := c.tr.Start(
		pipelineCtx,
		v.Name(),
		otrace.WithTimestamp(v.Started()),
		otrace.WithLinks(links...),
	)
	c.contextByVertex[v.ID()] = vertexCtx
	vertexSpan.SetAttributes(c.attributes(attribute.Bool("cached", v.Cached()), attribute.String("digest", v.ID()))...)

	if err := v.Error(); err != nil {
		vertexSpan.RecordError(err)
		vertexSpan.SetStatus(codes.Error, err.Error())
	}
	vertexSpan.End(otrace.WithTimestamp(v.Completed()))
}

func (c *TraceExporter) pipelineContext(v Vertex) context.Context {
	ctx := c.rootCtx
	pipeline := v.Pipeline()
	for i := range pipeline {
		parent := pipeline[0 : i+1]
		parentCtx := c.contextByPipeline[parent.ID()]
		if parentCtx == nil {
			parentVertices := c.verticesForPipeline(parent)
			var parentSpan otrace.Span
			parentCtx, parentSpan = c.tr.Start(ctx,
				pipeline[i].Name,
				otrace.WithTimestamp(parentVertices.Started()),
			)
			parentSpan.SetAttributes(c.attributes()...)
			parentSpan.End(otrace.WithTimestamp(parentVertices.Completed()))

			c.contextByPipeline[parent.ID()] = parentCtx
		}
		ctx = parentCtx
	}

	return ctx
}

func (c *TraceExporter) attributes(attributes ...attribute.KeyValue) []attribute.KeyValue {
	for k, v := range c.tags {
		attributes = append(attributes, attribute.String(k, v))
	}
	return attributes
}

func (c *TraceExporter) verticesForPipeline(selector core.PipelinePath) VertexList {
	matches := VertexList{}
	for _, v := range c.vertices {
		if matchPipeline(v, selector) {
			matches = append(matches, v)
		}
	}
	return matches
}

func matchPipeline(v Vertex, selector core.PipelinePath) bool {
	pipeline := v.Pipeline()
	if len(selector) > len(pipeline) {
		return false
	}
	for i, sel := range selector {
		if pipeline[i].Name != sel.Name {
			return false
		}
	}

	return true
}

func (c *TraceExporter) TraceID() string {
	if c.rootSpan == nil {
		return ""
	}
	return c.rootSpan.SpanContext().TraceID().String()
}

func newExporter() (trace.SpanExporter, error) {
	return otlptracegrpc.New(context.Background())
}

func newResource() *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("dagger"),
		semconv.ServiceVersionKey.String("v0.1.0"),
		attribute.String("environment", "test"),
	)
}
