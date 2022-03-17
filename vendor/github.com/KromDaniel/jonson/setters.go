/*
	Written by Daniel Krom
	2018
*/
package jonson

/*
Sets a value to the current JSON,
Makes a deep copy of the interface, removing the original reference
*/
func (jsn *JSON) Set(v interface{}) *JSON {
	jsn.rwMutex.Lock()
	defer jsn.rwMutex.Unlock()

	temp := jonsonize(v)
	jsn.kind = temp.kind
	jsn.value = temp.value
	jsn.isPrimitive = temp.isPrimitive
	return jsn
}

/*
Set a value to MapObject
if key doesn't exists, it creates it

if current json is not map, it does nothing
*/
func (jsn *JSON) MapSet(key string, value interface{}) *JSON {
	if !jsn.IsMap() {
		return jsn
	}
	jsn.rwMutex.Lock()
	defer jsn.rwMutex.Unlock()
	jsn.value.(map[string]*JSON)[key] = jonsonize(value)
	return jsn
}

func (jsn *JSON) DeleteMapKey(key string) *JSON {
	if !jsn.IsMap() {
		return jsn
	}
	jsn.rwMutex.Lock()
	defer jsn.rwMutex.Unlock()
	delete(jsn.value.(map[string]*JSON), key)
	return jsn
}

/*
Append a value at the end of the slice

if current json slice, it does nothing

multiple values will append in the order of the values
SliceAppend(1,2,3,4) -> [oldSlice..., 1,2,3,4]
*/
func (jsn *JSON) SliceAppend(value ...interface{}) *JSON {
	if !jsn.IsSlice() {
		return jsn
	}
	/*jsn.rwMutex.Lock()
	defer jsn.rwMutex.Unlock()
	*/
	for _, v := range value {
		jsn.value = append(jsn.value.([]*JSON), jonsonize(v))
	}

	return jsn
}

/*
Append a value at the start of the slice

if current json slice, it does nothing
multiple values will append begin in the order of the values
SliceAppend(1,2,3,4) -> [4,3,2,1, oldSlice...]
*/
func (jsn *JSON) SliceAppendBegin(value ...interface{}) *JSON {
	if !jsn.IsSlice() {
		return jsn
	}
	jsn.rwMutex.Lock()
	defer jsn.rwMutex.Unlock()
	arr := jsn.value.([]*JSON)
	for _, v := range value {
		arr = append([]*JSON{jonsonize(v)}, arr...)
	}
	jsn.value = arr

	return jsn
}

/*
Sets a value at index to current slice

if value isn't slice, it does nothing
User must make sure the length of the slice contains the index
*/
func (jsn *JSON) SliceSet(index int, value interface{}) *JSON {
	if !jsn.IsSlice() {
		return jsn
	}
	jsn.rwMutex.Lock()
	defer jsn.rwMutex.Unlock()

	jsn.value.([]*JSON)[index] = jonsonize(value)
	return jsn
}
