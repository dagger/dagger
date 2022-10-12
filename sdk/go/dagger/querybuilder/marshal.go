package querybuilder

import (
	"fmt"
	"reflect"
	"strings"
)

func MarshalGQL(v any) string {
	return marshalGQL(reflect.ValueOf(v))
}

func marshalGQL(v reflect.Value) string {
	t := v.Type()

	switch t.Kind() {
	case reflect.Bool:
		return fmt.Sprintf("%t", v.Bool())
	case reflect.Int:
		return fmt.Sprintf("%d", v.Int())
	case reflect.String:
		return fmt.Sprintf("%q", v.String())
	case reflect.Pointer:
		if v.IsNil() {
			return "null"
		}
		return marshalGQL(v.Elem())
	case reflect.Slice:
		encoded := "["
		n := v.Len()
		for i := 0; i < n; i++ {
			if i > 0 {
				encoded += ","
			}
			encoded += marshalGQL(v.Index(i))
		}
		encoded += "]"
		return encoded
	case reflect.Struct:
		encoded := "{"
		for i := 0; i < v.NumField(); i++ {
			if i > 0 {
				encoded += ","
			}

			f := t.Field(i)
			name := f.Name
			tag := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
			if tag != "" {
				name = tag
			}
			encoded += fmt.Sprintf("%s:%s", name, marshalGQL(v.Field(i)))
		}
		encoded += "}"
		return encoded
	default:
		panic(fmt.Errorf("unsupported argument of kind %s", t.Kind()))
	}
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
