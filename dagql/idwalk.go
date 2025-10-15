package dagql

import (
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

func WalkID(id *call.ID, skipTopLevel bool) (*IDWalker, error) {
	idWalker := &IDWalker{
		typeNameToIDs: map[string][]*call.ID{},
		memo:          map[digest.Digest]struct{}{},
	}
	if err := idWalker.walkID(id, skipTopLevel); err != nil {
		return nil, err
	}
	return idWalker, nil
}

type IDWalker struct {
	typeNameToIDs map[string][]*call.ID
	memo          map[digest.Digest]struct{}
}

func WalkedIDs[T Typed](idWalker *IDWalker) []ID[T] {
	var t T
	callIDs := idWalker.typeNameToIDs[t.Type().Name()]
	if len(callIDs) == 0 {
		return nil
	}
	typedIDs := make([]ID[T], 0, len(callIDs))
	for _, callID := range callIDs {
		typedIDs = append(typedIDs, NewID[T](callID))
	}
	return typedIDs
}

func (idWalker *IDWalker) walkID(id *call.ID, skipCurrent bool) error {
	dgst := id.Digest()
	if _, ok := idWalker.memo[dgst]; ok {
		return nil
	}
	idWalker.memo[dgst] = struct{}{}

	if !skipCurrent {
		if typeName := id.Type().NamedType(); typeName != "" {
			idWalker.typeNameToIDs[typeName] = append(idWalker.typeNameToIDs[typeName], id)
		}
	}

	if recv := id.Receiver(); recv != nil {
		if err := idWalker.walkID(recv, false); err != nil {
			return err
		}
	}

	for _, arg := range id.Args() {
		if err := idWalker.walkLiteral(arg.Value()); err != nil {
			return err
		}
	}

	return nil
}

func (idWalker *IDWalker) walkLiteral(lit call.Literal) error {
	switch x := lit.(type) {
	case *call.LiteralID:
		return idWalker.walkID(x.Value(), false)
	case *call.LiteralList:
		for _, v := range x.Values() {
			if err := idWalker.walkLiteral(v); err != nil {
				return err
			}
		}
	case *call.LiteralObject:
		for _, v := range x.Args() {
			if err := idWalker.walkLiteral(v.Value()); err != nil {
				return err
			}
		}
	default:
		// NOTE: not handling any primitive types right now, could be added
		// if needed for some reason (i.e. you want every int in an id?)
	}
	return nil
}
