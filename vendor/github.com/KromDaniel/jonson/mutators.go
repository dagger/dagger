/*
	Written by Daniel Krom
	2018
*/
package jonson

import (
	"reflect"
	"strconv"
	"errors"

)

type numberConvertion uint8

const (
	toInt         numberConvertion = 0
	toFloat       numberConvertion = 1
	toUnsignedInt numberConvertion = 2
	toString      numberConvertion = 3
)

func (jsn *JSON) MutateToInt() (success bool) {
	success, val := jsn.convertToNumberType(toInt)
	if success {
		jsn.Set(val)
	}
	return
}

func (jsn *JSON) MutateToFloat() (success bool) {
	success, val := jsn.convertToNumberType(toFloat)
	if success {
		jsn.Set(val)
	}
	return
}

func (jsn *JSON) MutateToUnsignedInt() (success bool) {
	success, val := jsn.convertToNumberType(toUnsignedInt)
	if success {
		jsn.Set(val)
	}
	return
}

func (jsn *JSON) MutateToString() (success bool) {
	success, val := jsn.convertToNumberType(toString)
	if success {
		jsn.Set(val)
	}
	return
}

func (jsn *JSON) convertToNumberType(numType numberConvertion) (success bool, val interface{}) {
	jsn.rwMutex.RLock()
	defer jsn.rwMutex.RUnlock()
	if !jsn.isPrimitive {
		success = false
		return
	}
	success = true
	switch jsn.kind {
	case reflect.Float64:
		float64Value := jsn.GetUnsafeFloat64()
		switch numType {
		case toInt:
			val = int(float64Value)
			break
		case toFloat:
			val = float64Value
			break
		case toUnsignedInt:
			val = uint(float64Value)
			break
		case toString:
			val = strconv.FormatFloat(float64Value, 'E', -1, 64)
			break
		default:
			success = false
		}
		break
	case reflect.Float32:
		float32Value := jsn.GetUnsafeFloat32()
		switch numType {
		case toInt:
			val = int(float32Value)
			break
		case toFloat:
			val = float64(float32Value)
			break
		case toUnsignedInt:
			val = uint(float32Value)
			break
		case toString:
			val = strconv.FormatFloat(float64(float32Value), 'E', -1, 32)
			break
		default:
			success = false
		}
		break
	case reflect.Uint:
		unsignedIntValue := jsn.GetUnsafeUint()
		switch numType {
		case toInt:
			val = int(unsignedIntValue)
			break
		case toFloat:
			val = float64(unsignedIntValue)
			break
		case toUnsignedInt:
			val = unsignedIntValue
			break
		case toString:
			val = strconv.FormatUint(uint64(unsignedIntValue), 10)
			break
		default:
			success = false
		}
		break
	case reflect.Uint8:
		unsignedInt8Value := jsn.GetUnsafeUint8()
		switch numType {
		case toInt:
			val = int(unsignedInt8Value)
			break
		case toFloat:
			val = float64(unsignedInt8Value)
			break
		case toUnsignedInt:
			val = uint(unsignedInt8Value)
			break
		case toString:
			val = strconv.FormatUint(uint64(unsignedInt8Value), 10)
			break
		default:
			success = false
		}
		break
	case reflect.Uint16:
		unsignedInt16Value := jsn.GetUnsafeUint16()
		switch numType {
		case toInt:
			val = int(unsignedInt16Value)
			break
		case toFloat:
			val = float64(unsignedInt16Value)
			break
		case toUnsignedInt:
			val = uint(unsignedInt16Value)
			break
		case toString:
			val = strconv.FormatUint(uint64(unsignedInt16Value), 10)
			break
		default:
			success = false
		}
		break
	case reflect.Uint32:
		unsignedInt32Value := jsn.GetUnsafeUint32()
		switch numType {
		case toInt:
			val = int(unsignedInt32Value)
			break
		case toFloat:
			val = float64(unsignedInt32Value)
			break
		case toUnsignedInt:
			val = uint(unsignedInt32Value)
			break
		case toString:
			val = strconv.FormatUint(uint64(unsignedInt32Value), 10)
			break
		default:
			success = false
		}
		break
	case reflect.Uint64:
		unsignedInt64Value := jsn.GetUnsafeUint64()
		switch numType {
		case toInt:
			val = int(unsignedInt64Value)
			break
		case toFloat:
			val = float64(unsignedInt64Value)
			break
		case toUnsignedInt:
			val = uint(unsignedInt64Value)
			break
		case toString:
			val = strconv.FormatUint(unsignedInt64Value, 10)
			break
		default:
			success = false
		}
		break
	case reflect.Int:
		intValue := jsn.GetUnsafeInt()
		switch numType {
		case toInt:
			val = intValue
			break
		case toFloat:
			val = float64(intValue)
			break
		case toUnsignedInt:
			val = uint(intValue)
			break
		case toString:
			val = strconv.FormatInt(int64(intValue), 10)
			break
		default:
			success = false
		}
	case reflect.Int8:
		int8Value := jsn.GetUnsafeInt8()
		switch numType {
		case toInt:
			val = int(int8Value)
			break
		case toFloat:
			val = float64(int8Value)
			break
		case toUnsignedInt:
			val = uint(int8Value)
			break
		case toString:
			val = strconv.FormatInt(int64(int8Value), 10)
			break
		default:
			success = false
		}
		break
	case reflect.Int16:
		int16Value := jsn.GetUnsafeInt16()
		switch numType {
		case toInt:
			val = int(int16Value)
			break
		case toFloat:
			val = float64(int16Value)
			break
		case toUnsignedInt:
			val = uint(int16Value)
			break
		case toString:
			val = strconv.FormatInt(int64(int16Value), 10)
			break
		default:
			success = false
		}
		break
	case reflect.Int32:
		int32Value := jsn.GetUnsafeInt32()
		switch numType {
		case toInt:
			val = int(int32Value)
			break
		case toFloat:
			val = float64(int32Value)
			break
		case toUnsignedInt:
			val = uint(int32Value)
			break
		case toString:
			val = strconv.FormatInt(int64(int32Value), 10)
			break
		default:
			success = false
		}
		break
	case reflect.Int64:
		int64Value := jsn.GetUnsafeInt64()
		switch numType {
		case toInt:
			val = int(int64Value)
			break
		case toFloat:
			val = float64(int64Value)
			break
		case toUnsignedInt:
			val = uint(int64Value)
			break
		case toString:
			val = strconv.FormatInt(int64Value, 10)
			break
		default:
			success = false
		}
		break
	case reflect.String:
		str := jsn.GetUnsafeString()
		var err error
		switch numType {
		case toInt:
			val, err = strconv.ParseInt(str, 10, 64)
			if err == nil {
				val = int(val.(int64))
			}
			break
		case toFloat:
			val, err = strconv.ParseFloat(str, 64)
			break
		case toUnsignedInt:
			val, err = strconv.ParseUint(str, 10, 64)
			if err == nil {
				val = uint(val.(uint64))
			}
			break
		case toString:
			val = str
			break
		default:
			err = errors.New("unsupported type")
		}
		success = err == nil
		break
	default:
		success = false
	}

	return
}
