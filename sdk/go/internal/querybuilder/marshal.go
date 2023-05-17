package querybuilder

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"golang.org/x/sync/errgroup"
)

// GraphQLMarshaller is an internal interface for marshalling an object into GraphQL.
type GraphQLMarshaller interface {
	// XXX_GraphQLType is an internal function. It returns the native GraphQL type name
	XXX_GraphQLType() string
	// XXX_GraphqlID is an internal function. It returns the underlying type ID
	XXX_GraphQLID(ctx context.Context) (string, error)
}

const (
	GraphQLMarshallerType = "XXX_GraphQLType"
	GraphQLMarshallerID   = "XXX_GraphQLID"
)

var (
	gqlMarshaller reflect.Type

	// Taken from codegen/generator/functions.go
	// Includes also Platform
	customScalar = map[string]struct{}{
		"ContainerID":      {},
		"FileID":           {},
		"DirectoryID":      {},
		"SecretID":         {},
		"SocketID":         {},
		"CacheID":          {},
		"Platform":         {},
		"ProjectID":        {},
		"ProjectCommandID": {},
	}
)

func init() {
	gqlMarshaller = reflect.TypeOf((*GraphQLMarshaller)(nil)).Elem()
}

func MarshalGQL(ctx context.Context, v any) (string, error) {
	return marshalValue(ctx, reflect.ValueOf(v))
}

func marshalValue(ctx context.Context, v reflect.Value) (string, error) {
	t := v.Type()

	if t.Implements(gqlMarshaller) {
		return marshalCustom(ctx, v)
	}

	switch t.Kind() {
	case reflect.Bool:
		return fmt.Sprintf("%t", v.Bool()), nil
	case reflect.Int:
		return fmt.Sprintf("%d", v.Int()), nil
	case reflect.String:
		name := t.Name()
		// distinguish enum const values and customScalars from string type
		// GraphQL complains if you try to put a string literal in place of an enum: FOO vs "FOO"
		_, found := customScalar[t.Name()]
		if name != "string" && !found {
			return fmt.Sprintf("%s", v.String()), nil //nolint:gosimple,staticcheck
		}
		return fmt.Sprintf("%q", v.String()), nil //nolint:gosimple,staticcheck
	case reflect.Pointer:
		if v.IsNil() {
			return "null", nil
		}
		return marshalValue(ctx, v.Elem())
	case reflect.Slice:
		n := v.Len()
		elems := make([]string, n)
		eg, gctx := errgroup.WithContext(ctx)
		for i := 0; i < n; i++ {
			i := i
			eg.Go(func() error {
				m, err := marshalValue(gctx, v.Index(i))
				if err != nil {
					return err
				}
				elems[i] = m
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return "", err
		}
		return fmt.Sprintf("[%s]", strings.Join(elems, ",")), nil
	case reflect.Struct:
		n := v.NumField()
		elems := make([]string, n)
		eg, gctx := errgroup.WithContext(ctx)
		for i := 0; i < n; i++ {
			i := i
			eg.Go(func() error {
				f := t.Field(i)
				name := f.Name
				tag := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
				if tag != "" {
					name = tag
				}
				m, err := marshalValue(gctx, v.Field(i))
				if err != nil {
					return err
				}
				elems[i] = fmt.Sprintf("%s:%s", name, m)
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return "", err
		}
		return fmt.Sprintf("{%s}", strings.Join(elems, ",")), nil
	default:
		panic(fmt.Errorf("unsupported argument of kind %s", t.Kind()))
	}
}

func marshalCustom(ctx context.Context, v reflect.Value) (string, error) {
	result := v.MethodByName(GraphQLMarshallerID).Call([]reflect.Value{
		reflect.ValueOf(ctx),
	})
	if len(result) != 2 {
		panic(result)
	}
	err := result[1].Interface()
	if err != nil {
		return "", err.(error)
	}

	return fmt.Sprintf("%q", result[0].String()), nil
}

func IsZeroValue(value any) bool {
	v := reflect.ValueOf(value)
	kind := v.Type().Kind()
	switch kind {
	case reflect.Pointer:
		return v.IsNil()
	case reflect.Slice, reflect.Array:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}
