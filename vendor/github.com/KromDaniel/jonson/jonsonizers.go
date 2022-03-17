/*
	Written by Daniel Krom
	2018
*/
package jonson

import (
	"reflect"
)

func jonsonize(value interface{}) *JSON {
	if value == nil {
		return NewEmptyJSON()
	}
	vo := reflect.ValueOf(value)
	if vo.Kind() == reflect.Ptr {
		vo = vo.Elem()
		value = vo.Interface()
	}
	switch vo.Kind() {
	case reflect.Ptr:
		return jonsonize(vo.Elem())
	case reflect.Map:
		return jonsonizeMap(&vo)
	case reflect.Slice:
		return jonsonizeSlice(&vo)
	case reflect.String,
		reflect.Bool,
		reflect.Float64,
		reflect.Float32,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64:
		return &JSON{
			value:       value,
			isPrimitive: true,
			kind:        vo.Kind(),
		}
	case reflect.Struct:
		if v, ok := value.(JSON); ok {
			return v.Clone()
		}
		return jonsonizeStruct(&vo)
	}

	return NewEmptyJSON()
}

func jonsonizeMap(value *reflect.Value) *JSON {
	mapValue := make(map[string]*JSON)
	for _, k := range value.MapKeys() {
		// map should be only string as keys
		keyType := reflect.TypeOf(k.Interface())
		if keyType.Kind() != reflect.String {
			continue
		}

		mapValue[k.Interface().(string)] = jonsonize(value.MapIndex(k).Interface())
	}

	return &JSON{
		value:       mapValue,
		isPrimitive: false,
		kind:        reflect.Map,
	}
}

func jonsonizeSlice(value *reflect.Value) *JSON {
	arrValue := make([]*JSON, value.Len())
	for i := 0; i < value.Len(); i++ {
		arrValue[i] = jonsonize(value.Index(i).Interface())
	}

	return &JSON{
		value:       arrValue,
		isPrimitive: false,
		kind:        reflect.Slice,
	}
}

func jonsonizeStruct(vo *reflect.Value) *JSON {
	tempMap := make(map[string]interface{})
	typ := vo.Type()
	for i := 0; i < typ.NumField(); i++ {
		if vo.Field(i).CanInterface() {
			fieldValue := typ.Field(i)
			if v, has := fieldValue.Tag.Lookup("json"); has {
				if v != "-" {
					tempMap[v] = vo.Field(i).Interface()
				}
				continue
			}
			tempMap[fieldValue.Name] = vo.Field(i).Interface()
		}
	}
	return jonsonize(&tempMap)
}
