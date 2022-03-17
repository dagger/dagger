/*
	Written by Daniel Krom
	2018
*/
package jonson

import (
	"reflect"
	"sync"
)

type JSON struct {
	rwMutex     sync.RWMutex
	isPrimitive bool
	kind        reflect.Kind
	value       interface{}
}
