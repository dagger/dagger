package dagql

/* TODO: re-add once ready
// TODO: doc
func ReferencedTypes[T Typed](ctx context.Context, id *idproto.ID, srv *Server) ([]T, error) {
	ids := idWalker{
		typeNameToIDs: map[string][]*idproto.ID{},
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
			typedIDs = append(typedIDs, NewID[T](idProto))
		}
	}

	return LoadIDs[T](ctx, srv, typedIDs)
}

type idWalker struct {
	typeNameToIDs map[string][]*idproto.ID
	memo          map[digest.Digest]struct{}
}

func (idWalker *idWalker) walkID(id *idproto.ID) error {
	dgst, err := id.Digest()
	if err != nil {
		return fmt.Errorf("failed to digest ID: %w", err)
	}
	if _, ok := idWalker.memo[dgst]; ok {
		return nil
	}
	idWalker.memo[dgst] = struct{}{}

	idWalker.typeNameToIDs[id.UnderlyingTypeName()] = append(idWalker.typeNameToIDs[id.UnderlyingTypeName()], id)

	if id.Base != nil {
		if err := idWalker.walkID(id); err != nil {
			return err
		}
	}

	for _, arg := range id.Args {
		if err := idWalker.walkLiteral(arg.Value); err != nil {
			return err
		}
	}

	return nil
}

func (idWalker *idWalker) walkLiteral(lit *idproto.Literal) error {
	switch x := lit.Value.(type) {
	case *idproto.Literal_Id:
		return idWalker.walkID(x.Id)
	case *idproto.Literal_List:
		for _, v := range x.List.Values {
			if err := idWalker.walkLiteral(v); err != nil {
				return err
			}
		}
	case *idproto.Literal_Object:
		for _, v := range x.Object.Values {
			if err := idWalker.walkLiteral(v.Value); err != nil {
				return err
			}
		}
	default:
		// NOTE: not handling any primitive types right now, could be added
		// if needed for some reason
	}
	return nil
}
*/
