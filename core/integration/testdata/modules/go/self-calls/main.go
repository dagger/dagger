package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {
	Message string
}

func New() *Test {
	return &Test{Message: "hello from field"}
}

func (m *Test) ContainerEcho(
	// +optional
	// +default="Hello Self Calls"
	stringArg string,
) *dagger.Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}

func (m *Test) Print(ctx context.Context, stringArg string) (string, error) {
	return dag.Test().ContainerEcho(dagger.TestContainerEchoOpts{
		StringArg: stringArg,
	}).Stdout(ctx)
}

func (m *Test) PrintDefault(ctx context.Context) (string, error) {
	return dag.Test().ContainerEcho().Stdout(ctx)
}

type Color string

const (
	ColorRed   Color = "red"
	ColorGreen Color = "green"
)

// Describe is invoked through a self-call with an enum argument. It exercises
// the enum value names emitted into the self-call schema: the engine exposes
// enum values in SCREAMING_SNAKE, so a wrong-cased emitter would send an
// unknown wire value and the self-call would fail.
func (m *Test) Describe(color Color) string {
	if color == ColorGreen {
		return "got green"
	}
	return "got " + string(color)
}

// DescribeSelf self-calls Describe, passing a generated enum value. The
// binding namespaces the module's own types like the engine does, so the Go
// enum Color surfaces as dagger.TestColor.
func (m *Test) DescribeSelf(ctx context.Context) (string, error) {
	return dag.Test().Describe(ctx, dagger.TestColorGreen)
}

// Box is a secondary object type: self-call codegen must expose it under the
// name the engine installs (TestBox), or the generated binding and the engine
// schema disagree.
type Box struct {
	Tag string
}

func (m *Test) MakeBox(tag string) *Box {
	return &Box{Tag: tag}
}

// BoxedTag self-calls MakeBox and reads a field off the returned secondary
// object, proving object namespacing works end to end.
func (m *Test) BoxedTag(ctx context.Context) (string, error) {
	return dag.Test().MakeBox("boxed").Tag(ctx)
}

func (m *Test) BoxTag(box *Box) string {
	return box.Tag
}

// RelabeledTag self-calls BoxTag with an object argument, which travels as an
// ID stamped with @expectedType.
func (m *Test) RelabeledTag(ctx context.Context) (string, error) {
	return dag.Test().BoxTag(ctx, dag.Test().MakeBox("relabeled"))
}
