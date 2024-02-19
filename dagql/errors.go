package dagql

import "fmt"

type PanicError struct {
	Cause     any
	Self      Object
	Selection Selection
	Stack     []byte
}

func (err PanicError) Error() string {
	return fmt.Sprintf("panic while resolving %s.%s: %v\n\n%s",
		err.Self.Type().Name(),
		err.Selection.Alias,
		err.Cause,
		string(err.Stack))
}
