package querybuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"golang.org/x/sync/errgroup"
)

// QueryBuilder represents a GraphQL query builder using a chain-based approach
type QueryBuilder struct {
	name     string
	alias    string
	args     map[string]*argument
	bind     any
	multiple bool

	// Support for multi-field selections
	fields        []string
	subSelections map[string]*QueryBuilder

	// inlineFragment is the type name for an inline fragment (... on TypeName).
	// When set, this step emits "... on TypeName" instead of a field name.
	// It does not add a nesting level in the response — unpack skips it.
	inlineFragment string

	prev *QueryBuilder

	client     graphql.Client
	isMutation bool
}

// Query creates a new QueryBuilder
func Query() *QueryBuilder {
	return &QueryBuilder{}
}

// Keep the old name for backward compatibility
func QueryV2() *QueryBuilder {
	return Query()
}

// Mutation creates a new QueryBuilder for a mutation operation.
func Mutation() *QueryBuilder {
	return &QueryBuilder{isMutation: true}
}

// Type alias for backward compatibility
type Selection = QueryBuilder

func (q *QueryBuilder) path() []*QueryBuilder {
	selections := []*QueryBuilder{}
	for sel := q; sel.prev != nil; sel = sel.prev {
		selections = append([]*QueryBuilder{sel}, selections...)
	}
	return selections
}

func (q *QueryBuilder) Root() *QueryBuilder {
	return &QueryBuilder{
		client:     q.client,
		isMutation: q.isMutation,
	}
}

func (q *QueryBuilder) SelectWithAlias(alias, name string) *QueryBuilder {
	sel := &QueryBuilder{
		name:       name,
		prev:       q,
		alias:      alias,
		client:     q.client,
		isMutation: q.isMutation,
	}
	return sel
}

func (q *QueryBuilder) Select(name string) *QueryBuilder {
	return q.SelectWithAlias("", name)
}

// InlineFragment adds an inline fragment type condition (... on TypeName).
// Subsequent selections will be nested inside the fragment. The response
// data is flat — the fragment doesn't add a nesting level during unpack.
func (q *QueryBuilder) InlineFragment(typeName string) *QueryBuilder {
	return &QueryBuilder{
		inlineFragment: typeName,
		prev:           q,
		client:         q.client,
		isMutation:     q.isMutation,
	}
}

func (q *QueryBuilder) SelectMultiple(name ...string) *QueryBuilder {
	sel := q.SelectWithAlias("", strings.Join(name, " "))
	sel.multiple = true
	return sel
}

// SelectFields selects multiple fields at the current level
func (q *QueryBuilder) SelectFields(fields ...string) *QueryBuilder {
	sel := &QueryBuilder{
		prev:          q,
		client:        q.client,
		isMutation:    q.isMutation,
		fields:        fields,
		subSelections: make(map[string]*QueryBuilder),
	}
	return sel
}

// SelectNested selects a field with nested sub-selections
func (q *QueryBuilder) SelectNested(field string, subSelection *QueryBuilder) *QueryBuilder {
	sel := &QueryBuilder{
		prev:          q,
		client:        q.client,
		isMutation:    q.isMutation,
		subSelections: make(map[string]*QueryBuilder),
	}
	sel.subSelections[field] = subSelection
	return sel
}

// SelectMixed allows mixing simple fields and nested selections at the same level
func (q *QueryBuilder) SelectMixed(simpleFields []string, nestedSelections map[string]*QueryBuilder) *QueryBuilder {
	sel := &QueryBuilder{
		prev:          q,
		client:        q.client,
		isMutation:    q.isMutation,
		fields:        simpleFields,
		subSelections: nestedSelections,
	}
	return sel
}

func (q *QueryBuilder) Arg(name string, value any) *QueryBuilder {
	sel := *q
	if sel.args == nil {
		sel.args = map[string]*argument{}
	}

	sel.args[name] = &argument{
		value: value,
	}
	return &sel
}

func (q *QueryBuilder) Bind(v any) *QueryBuilder {
	sel := *q
	sel.bind = v
	return &sel
}

func (q *QueryBuilder) Client(c graphql.Client) *QueryBuilder {
	sel := *q
	sel.client = c
	return &sel
}

func (q *QueryBuilder) marshalArguments(ctx context.Context) error {
	eg, gctx := errgroup.WithContext(ctx)
	for _, sel := range q.path() {
		for _, arg := range sel.args {
			eg.Go(func() error {
				return arg.marshal(gctx)
			})
		}
	}

	return eg.Wait()
}

func (q *QueryBuilder) Build(ctx context.Context) (string, error) {
	if err := q.marshalArguments(ctx); err != nil {
		return "", err
	}

	var b strings.Builder

	path := q.path()

	for _, sel := range path {
		if sel.prev != nil && sel.prev.multiple {
			return "", fmt.Errorf("sibling selections not end of chain")
		}

		b.WriteRune('{')

		// Handle multi-field selections (SelectFields) and mixed selections
		if len(sel.fields) > 0 || len(sel.subSelections) > 0 {
			// Write simple fields first
			for i, field := range sel.fields {
				if i > 0 {
					b.WriteRune(' ')
				}
				b.WriteString(field)
			}

			// Write nested selections
			needSpace := len(sel.fields) > 0
			for field, subSel := range sel.subSelections {
				if needSpace {
					b.WriteRune(' ')
				}
				b.WriteString(field)
				// Build sub-selection
				if subSel != nil {
					subQuery, err := subSel.Build(ctx)
					if err != nil {
						return "", err
					}
					b.WriteString(subQuery)
				}
				needSpace = true
			}
		} else if sel.inlineFragment != "" {
			b.WriteString("... on ")
			b.WriteString(sel.inlineFragment)
		} else {
			// Handle regular single field selection
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
	}

	b.WriteString(strings.Repeat("}", len(path)))
	return b.String(), nil
}

func (q *QueryBuilder) unpack(data any) error {
	for _, i := range q.path() {
		// Inline fragments don't add a nesting level in the response.
		if i.inlineFragment != "" {
			if i.bind != nil {
				marshalled, err := json.Marshal(data)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(marshalled, i.bind); err != nil {
					return err
				}
			}
			continue
		}

		k := i.name
		if i.alias != "" {
			k = i.alias
		}

		// Handle SelectFields case - when we have fields but no name,
		// or when we have subselections but no name (mixed selection case)
		// don't navigate deeper, just bind at the current level
		if (len(i.fields) > 0 || len(i.subSelections) > 0) && i.name == "" {
			// This is a SelectFields or mixed selection - bind directly to current data
			if i.bind != nil {
				marshalled, err := json.Marshal(data)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(marshalled, i.bind); err != nil {
					return err
				}
			}
			continue
		}

		if !i.multiple {
			if f, ok := data.(map[string]any); ok {
				data = f[k]
			}
		}

		if i.bind != nil {
			marshalled, err := json.Marshal(data)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(marshalled, i.bind); err != nil {
				return err
			}
		}
	}

	return nil
}

func (q *QueryBuilder) Execute(ctx context.Context) error {
	if q.client == nil {
		debug.PrintStack()
		return fmt.Errorf("no client configured for selection")
	}

	query, err := q.Build(ctx)
	if err != nil {
		return err
	}

	opType := "query"
	opName := "Query"
	if q.isMutation {
		opType = "mutation"
		opName = "Mutation"
	}

	var response any
	err = q.client.MakeRequest(ctx,
		&graphql.Request{
			Query:  opType + " " + opName + " " + query,
			OpName: opName,
		},
		&graphql.Response{Data: &response},
	)
	if err != nil {
		return err
	}

	return q.unpack(response)
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
