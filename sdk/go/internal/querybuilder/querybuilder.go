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
	bind  interface{}

	prev *Selection
}

func (s *Selection) path() []*Selection {
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

func (s *Selection) Bind(v interface{}) *Selection {
	sel := *s
	sel.bind = v
	return &sel
}

func (s *Selection) marshalArguments(ctx context.Context) error {
	eg, gctx := errgroup.WithContext(ctx)
	for _, sel := range s.path() {
		for _, arg := range sel.args {
			arg := arg
			eg.Go(func() error {
				return arg.marshal(gctx)
			})
		}
	}

	return eg.Wait()
}

func (s *Selection) build(ctx context.Context) (string, error) {
	if err := s.marshalArguments(ctx); err != nil {
		return "", err
	}
	fields := []string{
		"query",
	}
	for _, sel := range s.path() {
		q := sel.name
		if len(sel.args) > 0 {
			args := make([]string, 0, len(sel.args))
			for name, arg := range sel.args {
				args = append(args, fmt.Sprintf("%s:%s", name, arg.marshalled))
			}
			q += "(" + strings.Join(args, ", ") + ")"
		}
		if sel.alias != "" {
			q = sel.alias + ":" + q
		}
		fields = append(fields, q)
	}

	q := strings.Join(fields, "{") + strings.Repeat("}", len(fields)-1)
	return q, nil
}

func (s *Selection) unpack(data interface{}) error {
	for _, i := range s.path() {
		k := i.name
		if i.alias != "" {
			k = i.alias
		}

		// Try to asert type of the value
		switch f := data.(type) {
		case map[string]interface{}:
			data = data.(map[string]interface{})[k]
		case []interface{}:
			data = data.([]interface{})
		default:
			fmt.Printf("type not found %s\n", f)
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
	query, err := s.build(ctx)
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

	return s.unpack(response)
}

type argument struct {
	value      any
	marshalled string
	once       sync.Once
}

func (a *argument) marshal(ctx context.Context) error {
	var err error
	a.once.Do(func() {
		a.marshalled, err = MarshalGQL(ctx, a.value)
	})
	return err
}
