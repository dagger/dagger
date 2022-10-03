package querybuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"go.dagger.io/dagger/sdk/go/dagger"
)

func Query() *Selection {
	return &Selection{}
}

type gqlTyper interface {
	GQLType() string
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

func argumentKind(v any) string {
	switch v := v.(type) {
	case *bool:
		return "Boolean"
	case bool:
		return "Boolean!"
	case *[]*bool:
		return "[Boolean]"
	case []*bool:
		return "[Boolean]!"
	case *[]bool:
		return "[Boolean!]"
	case []bool:
		return "[Boolean!]!"
	case *int:
		return "Int"
	case int:
		return "Int!"
	case *[]*int:
		return "[Int]"
	case []*int:
		return "[Int]!"
	case *[]int:
		return "[Int!]"
	case []int:
		return "[Int!]!"
	case *string:
		return "String"
	case string:
		return "String!"
	case *[]*string:
		return "[String]"
	case []*string:
		return "[String]!"
	case *[]string:
		return "[String!]"
	case []string:
		return "[String!]!"

	case *gqlTyper:
		return (*v).GQLType()
	case gqlTyper:
		return v.GQLType() + "!"
	// FIXME: need to support these somehow
	// case *[]*gqlTyper:
	// 	return "[String]"
	// case []*gqlTyper:
	// 	return "[String]!"
	// case *[]gqlTyper:
	// 	return "[String!]"
	// case []gqlTyper:
	// 	return "[String!]!"

	default:
		panic(fmt.Errorf("unsupported argument of kind %T: %v", v, v))
	}
}

func (s *Selection) Build() (string, map[string]any) {
	fields := []string{}
	variables := map[string]any{}
	setVariable := func(name string, value any) string {
		for i := 1; i < 100; i++ {
			k := fmt.Sprintf("%s%d", name, i)
			if i == 1 {
				k = name
			}
			if _, ok := variables[k]; !ok {
				variables[k] = value
				return k
			}
		}
		panic("argument names exhausted")
	}
	for _, sel := range s.path() {
		q := sel.name
		if len(sel.args) > 0 {
			args := make([]string, 0, len(sel.args))
			for name, value := range sel.args {
				k := setVariable(name, value)
				args = append(args, fmt.Sprintf("%s:$%s", name, k))
			}
			q += "(" + strings.Join(args, ", ") + ")"
		}
		if sel.alias != "" {
			q = sel.alias + ":" + q
		}
		fields = append(fields, q)
	}

	// Generate top-level `query(...) {}`
	query := "query"
	queryArgs := []string{}
	for name, v := range variables {
		queryArgs = append(queryArgs, fmt.Sprintf("$%s: %s", name, argumentKind(v)))
	}
	if len(queryArgs) > 0 {
		query += "(" + strings.Join(queryArgs, ", ") + ")"
	}
	fields = append([]string{query}, fields...)

	q := strings.Join(fields, "{") + strings.Repeat("}", len(fields)-1)
	return q, variables
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
	query, vars := s.Build()

	fmt.Printf("QUERY: %s [args: %+v]\n", query, vars)

	cl, err := dagger.Client(ctx)
	if err != nil {
		return err
	}

	var response any
	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query:     query,
			Variables: vars,
		},
		&graphql.Response{Data: &response},
	)
	if err != nil {
		return err
	}

	return s.Unpack(response)
}
