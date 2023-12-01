package querybuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"golang.org/x/sync/errgroup"
)

func Query() *Selection {
	return &Selection{}
}

type Selection struct {
	name  string
	alias string
	args  map[string]*argument
	bind  any

	prev *Selection
}

func (s *Selection) Path() []*Selection {
	selections := []*Selection{}
	for sel := s; sel.prev != nil; sel = sel.prev {
		selections = append([]*Selection{sel}, selections...)
	}

	return selections
}

func (s *Selection) SelectWithAlias(alias, name string) *Selection {
	sel := &Selection{
		name:  name,
		prev:  s,
		alias: alias,
	}
	return sel
}

func (s *Selection) Select(name string) *Selection {
	return s.SelectWithAlias("", name)
}

func (s *Selection) Arg(name string, value any) *Selection {
	sel := *s
	if sel.args == nil {
		sel.args = map[string]*argument{}
	}

	sel.args[name] = &argument{
		value: value,
	}
	return &sel
}

func (s *Selection) Bind(v any) *Selection {
	sel := *s
	sel.bind = v
	return &sel
}

func (s *Selection) marshalArguments(ctx context.Context) error {
	eg, gctx := errgroup.WithContext(ctx)
	for _, sel := range s.Path() {
		for _, arg := range sel.args {
			arg := arg
			eg.Go(func() error {
				return arg.marshal(gctx)
			})
		}
	}

	return eg.Wait()
}

func (s *Selection) Build(ctx context.Context) (string, error) {
	if err := s.marshalArguments(ctx); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("query")

	path := s.Path()

	for _, sel := range path {
		b.WriteRune('{')

		if sel.alias != "" {
			b.WriteString(sel.alias)
			b.WriteRune(':')
		}

		b.WriteString(sel.name)

		if len(sel.args) > 0 {
			b.WriteRune('(')
			i := 0
			for name, arg := range sel.args {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(name)
				b.WriteRune(':')
				b.WriteString(arg.marshalled)
				i++
			}
			b.WriteRune(')')
		}
	}

	b.WriteString(strings.Repeat("}", len(path)))
	return b.String(), nil
}

func (s *Selection) Pack(data any) any {
	path := s.Path()
	for i := len(path) - 1; i >= 0; i-- {
		p := path[i]
		k := p.name
		if p.alias != "" {
			k = p.alias
		}
		data = map[string]any{k: data}
	}
	return data
}

func (s *Selection) Unpack(data any) error {
	for _, i := range s.Path() {
		k := i.name
		if i.alias != "" {
			k = i.alias
		}

		// Try to assert type of the value
		switch f := data.(type) {
		case map[string]any:
			data = f[k]
		case []any:
			data = f
		default:
			return fmt.Errorf("type not found: %v (%T)", f, f)
		}

		if i.bind != nil {
			marshalled, err := json.Marshal(data)
			if err != nil {
				return err
			}
			json.Unmarshal(marshalled, s.bind)
		}
	}

	return nil
}

func (s *Selection) Execute(ctx context.Context, c graphql.Client) error {
	query, err := s.Build(ctx)
	if err != nil {
		return err
	}

	var response any
	err = c.MakeRequest(ctx,
		&graphql.Request{
			Query: query,
		},
		&graphql.Response{Data: &response},
	)
	if err != nil {
		return err
	}

	return s.Unpack(response)
}

type argument struct {
	value any

	marshalled    string
	marshalledErr error
	once          sync.Once
}

func (a *argument) marshal(ctx context.Context) error {
	a.once.Do(func() {
		a.marshalled, a.marshalledErr = MarshalGQL(ctx, a.value)
	})
	return a.marshalledErr
}
