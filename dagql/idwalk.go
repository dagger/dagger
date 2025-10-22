package dagql

import (
	"cmp"
	"errors"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
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
			resID = cmp.Or(resID, id).With(call.WithReceiver(newRecv))
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

// CollectIDs walks the given ID and collects all unique IDs found, grouped by their type name.
func CollectIDs(id *call.ID, skipTopLevel bool) (*IDSet, error) {
	ids := &IDSet{
		typeNameToIDs: map[string][]*call.ID{},
		memo:          map[digest.Digest]struct{}{},
		skip:          id,
	}
	_, err := visitID(id, ids.visit, false)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

type IDSet struct {
	typeNameToIDs map[string][]*call.ID
	memo          map[digest.Digest]struct{}
	skip          *call.ID
}

func CollectedIDs[T Typed](ids *IDSet) []ID[T] {
	var t T
	callIDs := ids.typeNameToIDs[t.Type().Name()]
	if len(callIDs) == 0 {
		return nil
	}
	typedIDs := make([]ID[T], 0, len(callIDs))
	for _, callID := range callIDs {
		typedIDs = append(typedIDs, NewID[T](callID))
	}
	return typedIDs
}

func (ids *IDSet) visit(id *call.ID) (*call.ID, error) {
	dgst := id.Digest()
	if _, ok := ids.memo[dgst]; ok {
		return nil, ErrStopVisit
	}
	ids.memo[dgst] = struct{}{}

	if id == ids.skip {
		return nil, nil
	}

	if typeName := id.Type().NamedType(); typeName != "" {
		ids.typeNameToIDs[typeName] = append(ids.typeNameToIDs[typeName], id)
	}

	return nil, nil
}
