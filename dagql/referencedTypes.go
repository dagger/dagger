package dagql

import (
	"context"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

// TODO: doc, note that it excludes return of ID itself
func ReferencedTypes[T Typed](ctx context.Context, id *call.ID, srv *Server, includeSelf bool) ([]T, error) {
	ids := idWalker{
		typeNameToIDs: map[string][]*call.ID{},
		memo:          map[digest.Digest]struct{}{},
	}
	if err := ids.walkID(id); err != nil {
		return nil, err
	}

	var implementingTypes []T
	srv.installLock.Lock()
	for _, objType := range srv.objects {
		innerType := objType.Typed()
		if t, ok := innerType.(T); ok {
			implementingTypes = append(implementingTypes, t)
		}
	}
	srv.installLock.Unlock()

	if len(implementingTypes) == 0 {
		return nil, nil
	}

	var typedIDs []ID[T]
	for _, t := range implementingTypes {
		typeName := t.Type().Name()
		idProtos, ok := ids.typeNameToIDs[typeName]
		if !ok {
			return nil, nil
		}
		for _, idProto := range idProtos {
			if !includeSelf && idProto.Digest() == id.Digest() {
				// skip self
				continue
			}
			typedIDs = append(typedIDs, NewID[T](idProto))
		}
	}

	return LoadIDs[T](ctx, srv, typedIDs)
}

type idWalker struct {
	typeNameToIDs map[string][]*call.ID
	memo          map[digest.Digest]struct{}
}

func (idWalker *idWalker) walkID(id *call.ID) error {
	dgst := id.Digest()
	if _, ok := idWalker.memo[dgst]; ok {
		return nil
	}
	idWalker.memo[dgst] = struct{}{}

	namedType := id.Type().NamedType()
	if namedType != "" {
		idWalker.typeNameToIDs[namedType] = append(idWalker.typeNameToIDs[namedType], id)
	}

	if id.Base != nil {
		if err := idWalker.walkID(id); err != nil {
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

func (idWalker *idWalker) walkLiteral(lit call.Literal) error {
	switch x := lit.(type) {
	case *call.LiteralID:
		return idWalker.walkID(x.Value())
	case *call.LiteralList:
		if err := x.Range(func(_ int, v call.Literal) error {
			return idWalker.walkLiteral(v)
		}); err != nil {
			return err
		}
	case *call.LiteralObject:
		if err := x.Range(func(_ int, _ string, v call.Literal) error {
			return idWalker.walkLiteral(v)
		}); err != nil {
			return err
		}
	default:
		// NOTE: not handling any primitive types right now, could be added
		// if needed for some reason
	}
	return nil
}
