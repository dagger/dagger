// A module that uses every kind of generated registration code.
//
// It has a second paragraph, with `backticks`, "quotes" and a unicode dash — here.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"dagger/feature/internal/dagger"
)

// The main object, with its own comment.
type Feature struct {
	// A public field with a doc comment
	Greeting string
	// +private
	Secret string
	Count  int `json:"theCount"`
	Items  []string
	Sub    *Sub
	Mode   Mode
	Dir    *dagger.Directory
	hidden string
}

// The constructor's comment.
func New(
	// The greeting
	// +default="hello"
	greeting string,
	// +optional
	count int,
	// +optional
	// +defaultPath="/hack"
	// +ignore=["*.log", "!keep.log"]
	src *dagger.Directory,
	// +default="fast"
	mode Mode,
) *Feature {
	return &Feature{Greeting: greeting, Count: count, Dir: src, Mode: mode, Secret: "s", hidden: "h"}
}

// Mode says how fast to go.
type Mode string

const (
	// Go fast.
	ModeFast Mode = "fast"
	// Go slow.
	//
	// Deprecated: nobody goes slow.
	ModeSlow Mode = "slow"
	// +deprecated="Use fast"
	ModeOld Mode = "old"
)

// Plain has no doc on its members.
type Plain string

const (
	PlainA Plain = "a"
	PlainB Plain = "b"
)

// A secondary object.
//
// +deprecated="Use something else"
type Sub struct {
	// +deprecated="Do not read this"
	Name string
}

// Describe the sub-object.
func (s *Sub) Describe(
	// +default=3
	times int,
	// +default=["x", "y"]
	names []string,
	// +default=true
	flag bool,
) string {
	return fmt.Sprintf("%s %d %v %v", s.Name, times, names, flag)
}

// Duck is an interface a caller can pass.
type Duck interface {
	dagger.DaggerObject
	// Quack loudly.
	Quack(ctx context.Context, times int) (string, error)
	Friend() Duck
}

// Hello says hello.
func (m *Feature) Hello(name string) string {
	return m.Greeting + " " + name
}

// +deprecated="Use Hello"
func (m *Feature) OldHello() string { return "old" }

// Random is never cached.
//
// +cache="never"
func (m *Feature) Random() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SessionRandom is cached per session.
//
// +cache="session"
func (m *Feature) SessionRandom() string { return m.Random() }

// TTLRandom has a time to live.
//
// +cache="42m"
func (m *Feature) TTLRandom() string { return m.Random() }

// Lint is a check.
//
// +check
func (m *Feature) Lint(ctx context.Context) error { return nil }

// Regen is a generator.
//
// +generate
func (m *Feature) Regen() *dagger.Changeset {
	return dag.Directory().WithNewFile("x", "y").Changes(dag.Directory())
}

// Serve starts a service.
//
// +up
func (m *Feature) Serve() *dagger.Service {
	return dag.Container().From("alpine:3.20").WithExposedPort(80).AsService()
}

// Args covers argument kinds.
func (m *Feature) Args(
	ctx context.Context,
	// A required string.
	s string,
	i int,
	f float64,
	b bool,
	// +optional
	os *string,
	// +optional
	oi *int,
	list []string,
	ints []int,
	mode Mode,
	// +optional
	modes []Mode,
	d *dagger.Directory,
	// +optional
	file *dagger.File,
	sec *dagger.Secret,
	// +deprecated="Do not pass this"
	// +optional
	old string,
	// +default="linux/amd64"
	platform dagger.Platform,
	// +optional
	duck Duck,
	// +optional
	ducks []Duck,
	// +optional
	// +defaultAddress="alpine:3.20"
	ctr *dagger.Container,
) (string, error) {
	return strings.Join(list, ","), nil
}

// Returns covers return kinds.
func (m *Feature) ReturnsVoid(ctx context.Context) error { return nil }

func (m *Feature) ReturnsList() []string { return nil }

func (m *Feature) ReturnsObjects() []*Sub { return []*Sub{{Name: "a"}} }

func (m *Feature) ReturnsNested() [][]string { return nil }

func (m *Feature) ReturnsSub() *Sub { return &Sub{Name: "s"} }

func (m *Feature) ReturnsDir() *dagger.Directory { return dag.Directory() }

func (m *Feature) ReturnsPlain() Plain { return PlainA }

func (m *Feature) ReturnsOptional() *string { return nil }

func (m *Feature) ReturnsDuck(ctx context.Context) (Duck, error) { return nil, nil }

func (m *Feature) WithGreeting(greeting string) *Feature {
	m.Greeting = greeting
	return m
}

// ReadOwnSource reads this module's own source through its current module.
func (m *Feature) ReadOwnSource(ctx context.Context) (string, error) {
	return dag.CurrentModule().Source().File("dagger-module.toml").Contents(ctx)
}

// ReadOwnGenerated reads the module's generated context.
func (m *Feature) ReadOwnGenerated(ctx context.Context) ([]string, error) {
	return dag.CurrentModule().GeneratedContextDirectory().Entries(ctx)
}

// SelfCall calls this module through its own generated bindings.
func (m *Feature) SelfCall(ctx context.Context) (string, error) {
	return dag.Feature().Hello(ctx, "self")
}

// Assistant is an agent.
//
// +agent
func (m *Feature) Assistant(base *dagger.LLM) *dagger.LLM { return base }

// Scripts returns a collection.
func (m *Feature) Scripts() *Scripts { return &Scripts{Keys: []string{"a", "b"}} }

// Scripts is a collection, keyed by name.
//
// +collection
type Scripts struct {
	// The names.
	//
	// +keys
	Keys []string
	// +delta
	Selection *dagger.CollectionDelta
}

// Get returns one item.
//
// +get
func (s *Scripts) Get(key string) *Sub { return &Sub{Name: key} }
