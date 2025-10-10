package dagql

import (
	"cmp"
	"errors"

	"github.com/dagger/dagger/dagql/call"
)

// VisitorFunc is a function that is called for each ID visited by VisitID.
// If the function returns a non-nil ID, that ID will replace the original ID.
type VisitorFunc func(id *call.ID) (newID *call.ID, err error)

var ErrStopVisit = errors.New("stop visiting")

// VisitID walks the given ID and its children, calling the given VisitorFunc
// for each ID.
// - If the VisitorFunc returns a non-nil ID, that ID will replace the original ID.
// - If the VisitorFunc returns ErrStopVisit, the visit will stop for that ID and its children.
// - If the VisitorFunc returns any other error, the visit will stop and the error will be returned.
func VisitID(id *call.ID, fn VisitorFunc) (*call.ID, error) {
	newID, err := visitID(id, fn, true)
	if err != nil {
		return nil, err
	}
	if newID != nil {
		return newID, nil
	}
	return id, nil
}

func visitID(id *call.ID, fn VisitorFunc, mutable bool) (resID *call.ID, _ error) {
	resID, err := fn(id)
	if errors.Is(err, ErrStopVisit) {
		return resID, nil
	}
	if err != nil {
		return nil, err
	}

	if recv := cmp.Or(resID, id).Receiver(); recv != nil {
		newRecv, err := visitID(recv, fn, mutable)
		if err != nil {
			return nil, err
		}
		if newRecv != nil {
			resID = cmp.Or(resID, id).WithReceiver(newRecv)
		}
	}

	for _, arg := range cmp.Or(resID, id).Args() {
		newArg, err := visitLiteral(arg.Value(), fn, mutable)
		if err != nil {
			return nil, err
		}
		if newArg != nil {
			resID = cmp.Or(resID, id).WithArgument(call.NewArgument(arg.Name(), newArg, arg.IsSensitive()))
		}
	}

	return resID, nil
}

func visitLiteral(lit call.Literal, fn VisitorFunc, mutable bool) (call.Literal, error) {
	switch x := lit.(type) {
	case *call.LiteralID:
		id, err := visitID(x.Value(), fn, mutable)
		if err != nil {
			return nil, err
		}
		if mutable && id != nil {
			return call.NewLiteralID(id), nil
		}
		return nil, nil

	case *call.LiteralList:
		var values []call.Literal
		if mutable {
			values = make([]call.Literal, 0, x.Len())
		}
		dirty := false

		for _, v := range x.Values() {
			newV, err := visitLiteral(v, fn, mutable)
			if err != nil {
				return nil, err
			}
			if mutable {
				if newV != nil {
					dirty = true
					v = newV
				}
				values = append(values, v)
			}
		}

		if dirty {
			return call.NewLiteralList(values...), nil
		}
		return nil, nil

	case *call.LiteralObject:
		var args []*call.Argument
		if mutable {
			args = make([]*call.Argument, 0, x.Len())
		}
		dirty := false

		for _, arg := range x.Args() {
			newV, err := visitLiteral(arg.Value(), fn, mutable)
			if err != nil {
				return nil, err
			}
			if mutable {
				if newV != nil {
					dirty = true
					arg = call.NewArgument(arg.Name(), newV, arg.IsSensitive())
				}
				args = append(args, arg)
			}
		}

		if dirty {
			return call.NewLiteralObject(args...), nil
		}
		return nil, nil

	default:
		// NOTE: not handling any primitive types right now, could be added
		// if needed for some reason (i.e. you want every int in an id?)
		return nil, nil
	}
}
