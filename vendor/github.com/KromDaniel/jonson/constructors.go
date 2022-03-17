/*
	Written by Daniel Krom
	2018
*/

package jonson

import "encoding/json"

/*
New Jonson Object with the value
of the interface. the reference to interface is lost
and it is deeply cloned

Possible types:
Primitive
Map[string]interface{}
Slice
struct
*/
func New(value interface{}) *JSON {
	return jonsonize(value)
}

/*
NewEmptyJSON Creates a new empty Jonson object with null value
*/
func NewEmptyJSON() *JSON {
	return &JSON{
		value:       nil,
		isPrimitive: true,
	}
}

/*
NewEmptyJSONMap Creates a new empty Jonson object with empty map
{}
*/
func NewEmptyJSONMap() *JSON {
	return New(make(map[string]interface{}))
}

/*
NewEmptyJSONArray Creates a new empty Jonson object with empty array
[]
*/
func NewEmptyJSONArray() *JSON {
	return New(make([]interface{}, 0))
}

/*
Parse JSON returns err, nil if error
*/
func Parse(data []byte) (jsn *JSON, err error) {
	var m interface{}
	err = json.Unmarshal(data, &m)
	if err != nil {
		return
	}
	jsn = New(m)
	return
}

/*
ParseUnsafe JSON returns null json if error
*/
func ParseUnsafe(data []byte) (jsn *JSON) {
	jsn, _ = Parse(data)
	if jsn == nil {
		jsn = NewEmptyJSON()
	}
	return jsn
}
