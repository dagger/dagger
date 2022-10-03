package querybuilder

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Khan/genqlient/graphql"
	"github.com/pkg/errors"
	"github.com/udacity/graphb"
	"go.dagger.io/dagger/sdk/go/dagger"
)

func Query() *Selection {
	return &Selection{}
}

type Selection struct {
	name  string
	alias string
	args  map[string]any
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
		sel.args = map[string]any{}
	}

	sel.args[name] = value
	return &sel
}

func (s *Selection) Bind(v interface{}) *Selection {
	sel := *s
	sel.bind = v
	return &sel
}

func (s *Selection) resultName() string {
	if s.alias != "" {
		return s.alias
	}
	return s.name
}

func (s *Selection) rootField() *graphb.Field {
	var root *graphb.Field
	var parent *graphb.Field
	for _, i := range s.path() {
		field := graphb.MakeField(i.name)
		if root == nil {
			root = field
		}

		if i.alias != "" {
			field.Alias = i.alias
		}
		for name, value := range i.args {
			var (
				arg graphb.Argument
				err error
			)

			if v, ok := value.(graphb.Argument); ok {
				arg = graphb.ArgumentCustomType(name, v)
			} else {
				arg, err = graphb.ArgumentAny(name, value)
				if err != nil {
					panic(err)
				}
			}

			field = field.AddArguments(arg)
		}
		if parent != nil {
			parent.Fields = []*graphb.Field{field}
		}
		parent = field
	}

	return root
}

func (s *Selection) Build() (string, error) {
	q := graphb.MakeQuery(graphb.TypeQuery)
	q.SetFields(s.rootField())

	strCh, err := q.StringChan()
	if err != nil {
		return "", errors.WithStack(err)
	}
	return graphb.StringFromChan(strCh), nil
}

func (s *Selection) Unpack(data interface{}) error {
	for _, i := range s.path() {
		data = data.(map[string]interface{})[i.resultName()]

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

func (s *Selection) Execute(ctx context.Context) error {
	query, err := s.Build()
	if err != nil {
		return err
	}

	fmt.Printf("QUERY: %s\n", query)

	cl, err := dagger.Client(ctx)
	if err != nil {
		return err
	}

	var response any
	err = cl.MakeRequest(ctx,
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
