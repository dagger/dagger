/*
	Written by Daniel Krom
	2018
*/

package jonson

import (
	"encoding/json"
	"reflect"
)

/*
ToJSON converts Jonson to byte array (serialize)
*/
func (jsn *JSON) ToJSON() ([]byte, error) {
	return json.Marshal(jsn.ToInterface())
}

/*
ToUnsafeJSON converts Jonson to byte array (serialize)
returns empty byte array if error
*/
func (jsn *JSON) ToUnsafeJSON() (data []byte) {
	data, err := jsn.ToJSON()
	if err != nil {
		return []byte{}
	}
	return
}

/*
ToJSONString converts Jonson to json string
*/
func (jsn *JSON) ToJSONString() (string, error) {
	data, err := jsn.ToJSON()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

/*
ToUnsafeJSONString converts Jonson to json string
returns empty string if error
*/
func (jsn *JSON) ToUnsafeJSONString() string {
	data, err := jsn.ToJSON()
	if err != nil {
		return ""
	}
	return string(data)
}

/*
ToInterface returns the entire jonson tree as interface of the value
e.g
[Jonson(5), Jonson("str"), Jonson(map[string]Jonson)]
->
to [5, "str", map[string]interface{}]
*/
func (jsn *JSON) ToInterface() interface{} {
	if jsn.IsPrimitive() {
		return jsn.value
	}

	if jsn.IsSlice() {
		arr := jsn.GetUnsafeSlice()
		resArr := make([]interface{}, len(arr))
		for k, v := range arr {
			resArr[k] = v.ToInterface()
		}
		return resArr
	}

	if jsn.IsMap() {
		hMap := jsn.GetUnsafeMap()
		resMap := make(map[string]interface{})
		for k, v := range hMap {
			resMap[k] = v.ToInterface()
		}
		return &resMap
	}

	return nil
}

/*
Clone deep the jonson
*/
func (jsn *JSON) Clone() *JSON {
	if jsn.IsPrimitive() {
		return &JSON{
			value:       jsn.value,
			kind:        jsn.kind,
			isPrimitive: true,
		}
	}

	if jsn.IsSlice() {
		arr := jsn.GetUnsafeSlice()
		resArr := make([]*JSON, len(arr))
		for k, v := range arr {
			resArr[k] = v.Clone()
		}
		return &JSON{
			value:       resArr,
			kind:        reflect.Slice,
			isPrimitive: false,
		}
	}

	if jsn.IsMap() {
		hMap := jsn.GetUnsafeMap()
		resMap := make(map[string]*JSON)
		for k, v := range hMap {
			resMap[k] = v.Clone()
		}
		return &JSON{
			value:       resMap,
			kind:        reflect.Map,
			isPrimitive: false,
		}
	}

	return NewEmptyJSON()
}
