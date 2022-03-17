/*
	Written by Daniel Krom
	2018
*/
package jonson

import "reflect"

/*
At returns the jonson value at some path
can be chained or\and using multiple keys
String key will assume the jonson is object
int key will assume the jonson is slice
if the path is wrong the empty json is returned

Jonson.ParseUnsafe([]byte("{\"foo\" : \"bar\"}")).At("keyThatDoesNotExists").At("subKey", 3, 6 ,90)

At("key","subKey",5, 7) equals to .At("key").At(5).At(7) equals to At("key",5).At(7)
*/
func (jsn *JSON) At(key interface{}, keys ...interface{}) *JSON {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	res := jsn.atLocked(key, keys...)
	return res
}

/*
IsNumber returns a boolean indicates is the value is number, can be any valid number type
*/
func (jsn *JSON) IsNumber() bool {
	if !jsn.isPrimitive {
		return false
	}
	switch jsn.kind {
	case reflect.Float64,
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
		return true
	}
	return false
}

/*
IsType returns a boolean indicates if the Jonson value is that type
*/
func (jsn *JSON) IsType(p reflect.Kind) bool {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	return jsn.kind == p
}

/*
IsString -> is the value type is string
*/
func (jsn *JSON) IsString() bool {
	return jsn.IsType(reflect.String)
}

/*
IsInt -> is the value type is int (default int 64)
*/
func (jsn *JSON) IsInt() bool {
	return jsn.IsType(reflect.Int)
}

func (jsn *JSON) IsInt8() bool {
	return jsn.IsType(reflect.Int8)
}

func (jsn *JSON) IsInt16() bool {
	return jsn.IsType(reflect.Int16)
}

func (jsn *JSON) IsInt32() bool {
	return jsn.IsType(reflect.Int32)
}

func (jsn *JSON) IsInt64() bool {
	return jsn.IsType(reflect.Int64)
}

func (jsn *JSON) IsBool() bool {
	return jsn.IsType(reflect.Bool)
}

func (jsn *JSON) IsFloat32() bool {
	return jsn.IsType(reflect.Float32)
}

func (jsn *JSON) IsFloat64() bool {
	return jsn.IsType(reflect.Float64)
}

func (jsn *JSON) IsUint() bool {
	return jsn.IsType(reflect.Uint)
}

func (jsn *JSON) IsUint8() bool {
	return jsn.IsType(reflect.Uint8)
}

func (jsn *JSON) IsUint16() bool {
	return jsn.IsType(reflect.Uint16)
}

func (jsn *JSON) IsUint32() bool {
	return jsn.IsType(reflect.Uint32)
}

func (jsn *JSON) IsUint64() bool {
	return jsn.IsType(reflect.Uint64)
}

func (jsn *JSON) IsNil() bool {
	return jsn.value == nil
}

func (jsn *JSON) IsMap() bool {
	return jsn.IsType(reflect.Map)
}

func (jsn *JSON) IsSlice() bool {
	return jsn.IsType(reflect.Slice)
}

func (jsn *JSON) IsPrimitive() bool {
	return jsn.isPrimitive
}

func (jsn *JSON) GetInt() (isInt bool, value int) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isInt = jsn.kind == reflect.Int
	if isInt {
		value = jsn.value.(int)
	}
	return
}

func (jsn *JSON) GetUnsafeInt() (value int) {
	_, value = jsn.GetInt()
	return
}

func (jsn *JSON) GetInt8() (isInt8 bool, value int8) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isInt8 = jsn.kind == reflect.Int8
	if isInt8 {
		value = jsn.value.(int8)
	}
	return
}

func (jsn *JSON) GetUnsafeInt8() (value int8) {
	_, value = jsn.GetInt8()
	return
}

func (jsn *JSON) GetInt16() (isInt16 bool, value int16) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isInt16 = jsn.kind == reflect.Int16
	if isInt16 {
		value = jsn.value.(int16)
	}
	return
}

func (jsn *JSON) GetUnsafeInt16() (value int16) {
	_, value = jsn.GetInt16()
	return
}

func (jsn *JSON) GetInt32() (isInt32 bool, value int32) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isInt32 = jsn.kind == reflect.Int32
	if isInt32 {
		value = jsn.value.(int32)
	}
	return
}

func (jsn *JSON) GetUnsafeInt32() (value int32) {
	_, value = jsn.GetInt32()
	return
}

func (jsn *JSON) GetInt64() (isInt64 bool, value int64) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isInt64 = jsn.kind == reflect.Int64
	if isInt64 {
		value = jsn.value.(int64)
	}
	return
}

func (jsn *JSON) GetUnsafeInt64() (value int64) {
	_, value = jsn.GetInt64()
	return
}

func (jsn *JSON) GetFloat32() (isFloat32 bool, value float32) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isFloat32 = jsn.kind == reflect.Float32
	if isFloat32 {
		value = jsn.value.(float32)
	}
	return
}

func (jsn *JSON) GetUnsafeFloat32() (value float32) {
	_, value = jsn.GetFloat32()
	return
}

func (jsn *JSON) GetFloat64() (isFloat64 bool, value float64) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isFloat64 = jsn.kind == reflect.Float64
	if isFloat64 {
		value = jsn.value.(float64)
	}
	return
}

func (jsn *JSON) GetUnsafeFloat64() (value float64) {
	_, value = jsn.GetFloat64()
	return
}

func (jsn *JSON) GetBool() (isBool bool, value bool) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isBool = jsn.kind == reflect.Bool
	if isBool {
		value = jsn.value.(bool)
	}
	return
}

func (jsn *JSON) GetUnsafeBool() (value bool) {
	_, value = jsn.GetBool()
	return
}

func (jsn *JSON) GetString() (isString bool, value string) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isString = jsn.kind == reflect.String
	if isString {
		value = jsn.value.(string)
	}
	return
}

func (jsn *JSON) GetUnsafeString() (value string) {
	_, value = jsn.GetString()
	return
}

func (jsn *JSON) GetMap() (isMap bool, value map[string]*JSON) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isMap = jsn.kind == reflect.Map
	if isMap {
		value = jsn.value.(map[string]*JSON)
	}
	return
}

func (jsn *JSON) GetUnsafeMap() (value map[string]*JSON) {
	isMap, m := jsn.GetMap()
	if isMap {
		value = m
		return
	}
	value = make(map[string]*JSON)
	return
}

func (jsn *JSON) GetSlice() (isSlice bool, value []*JSON) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isSlice = jsn.kind == reflect.Slice
	if isSlice {
		value = jsn.value.([]*JSON)
	}

	return
}

func (jsn *JSON) GetUnsafeSlice() (value []*JSON) {
	isSlice, m := jsn.GetSlice()
	if isSlice {
		value = m
		return
	}
	value = make([]*JSON, 0)
	return
}

func (jsn *JSON) GetUint() (isUint bool, value uint) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isUint = jsn.kind == reflect.Uint
	if isUint {
		value = jsn.value.(uint)
	}
	return
}

func (jsn *JSON) GetUnsafeUint() (value uint) {
	_, value = jsn.GetUint()
	return
}

func (jsn *JSON) GetUint8() (isUint8 bool, value uint8) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isUint8 = jsn.kind == reflect.Uint8
	if isUint8 {
		value = jsn.value.(uint8)
	}
	return
}

func (jsn *JSON) GetUnsafeUint8() (value uint8) {
	_, value = jsn.GetUint8()
	return
}

func (jsn *JSON) GetUint16() (isUint16 bool, value uint16) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isUint16 = jsn.kind == reflect.Uint16
	if isUint16 {
		value = jsn.value.(uint16)
	}
	return
}

func (jsn *JSON) GetUnsafeUint16() (value uint16) {
	_, value = jsn.GetUint16()
	return
}

func (jsn *JSON) GetUint32() (isUint32 bool, value uint32) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isUint32 = jsn.kind == reflect.Uint32
	if isUint32 {
		value = jsn.value.(uint32)
	}
	return
}

func (jsn *JSON) GetUnsafeUint32() (value uint32) {
	_, value = jsn.GetUint32()
	return
}

func (jsn *JSON) GetUint64() (isUint64 bool, value uint64) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	isUint64 = jsn.kind == reflect.Int64
	if isUint64 {
		value = jsn.value.(uint64)
	}
	return
}

func (jsn *JSON) GetUnsafeUint64() (value uint64) {
	_, value = jsn.GetUint64()
	return
}

func (jsn *JSON) GetObjectKeys() []string {
	isMap, hMap := jsn.GetMap()
	if !isMap {
		return nil
	}

	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()

	keys := make([]string, len(hMap))
	i := 0
	for k := range hMap {
		keys[i] = k
		i++
	}
	return keys
}

func (jsn *JSON) ObjectKeyExists(key string) (exists bool) {
	isMap, hMap := jsn.GetMap()
	if !isMap {
		return false
	}

	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()

	_, exists = hMap[key]

	return
}

func (jsn *JSON) GetSliceLen() int {
	isSlice, slice := jsn.GetSlice()
	if !isSlice {
		return 0
	}

	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	return len(slice)
}

func (jsn *JSON) atLocked(key interface{}, keys ...interface{}) *JSON {
	var res *JSON
	switch reflect.TypeOf(key).Kind() {
	case reflect.Int, reflect.Uint:
		if jsn.IsSlice() {
			arr := jsn.value.([]*JSON)
			index := key.(int)
			if index < len(arr) {
				res = arr[index]
			}
		}
		break
	case reflect.String:
		if jsn.IsMap() {
			mapKey := key.(string)
			obj := jsn.value.(map[string]*JSON)
			if val, ok := obj[mapKey]; ok {
				res = val
			}
		}
		break
	}
	if len(keys) > 0 && res != nil {
		return res.atLocked(keys[0], keys[1:]...)
	}
	if res == nil {
		res = NewEmptyJSON()
	}
	return res
}
