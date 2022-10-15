package querybuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Khan/genqlient/graphql"
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

func (s *Selection) Build() string {
	fields := []string{
		"query",
	}
	for _, sel := range s.path() {
		q := sel.name
		if len(sel.args) > 0 {
			args := make([]string, 0, len(sel.args))
			for name, value := range sel.args {
				args = append(args, fmt.Sprintf("%s:%s", name, MarshalGQL(value)))
			}
			q += "(" + strings.Join(args, ", ") + ")"
		}
		if sel.alias != "" {
			q = sel.alias + ":" + q
		}
		fields = append(fields, q)
	}

	q := strings.Join(fields, "{") + strings.Repeat("}", len(fields)-1)
	return q
}

func (s *Selection) Unpack(data interface{}) error {
	for _, i := range s.path() {
		k := i.name
		if i.alias != "" {
			k = i.alias
		}
		data = data.(map[string]interface{})[k]

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
	query := s.Build()

	var response any
	err := c.MakeRequest(ctx,
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
