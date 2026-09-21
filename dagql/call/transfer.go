package call

import (
	"fmt"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// FilterTransferDigests copies the recipe DAG and retains only each vertex's
// explicitly transferable annotations. It does not consult an equality class.
func (id *ID) FilterTransferDigests() (*ID, error) {
	memo := map[*ID]*ID{}
	active := map[*ID]bool{}
	var copyID func(*ID) (*ID, error)
	var copyLiteral func(Literal) (Literal, error)
	copyArgs := func(args []*Argument) ([]*Argument, error) {
		copied := make([]*Argument, len(args))
		for i, arg := range args {
			if arg == nil {
				return nil, fmt.Errorf("nil recipe argument")
			}
			value, err := copyLiteral(arg.value)
			if err != nil {
				return nil, err
			}
			copied[i] = arg.WithValue(value)
		}
		return copied, nil
	}
	copyLiteral = func(lit Literal) (Literal, error) {
		switch lit := lit.(type) {
		case *LiteralID:
			id, err := copyID(lit.id)
			if err != nil {
				return nil, err
			}
			return NewLiteralID(id), nil
		case *LiteralList:
			values := make([]Literal, len(lit.values))
			for i, item := range lit.values {
				var err error
				values[i], err = copyLiteral(item)
				if err != nil {
					return nil, err
				}
			}
			return NewLiteralList(values...), nil
		case *LiteralObject:
			args, err := copyArgs(lit.values)
			if err != nil {
				return nil, err
			}
			return NewLiteralObject(args...), nil
		default:
			return lit, nil
		}
	}
	copyID = func(id *ID) (*ID, error) {
		if id == nil || id.IsHandle() {
			return id, nil
		}
		if active[id] {
			return nil, fmt.Errorf("recipe ID cycle")
		}
		if copy, ok := memo[id]; ok {
			return copy, nil
		}
		active[id] = true
		copy := id.shallowClone()
		var err error
		copy.receiver, err = copyID(id.receiver)
		if err != nil {
			return nil, err
		}
		copy.pb.ReceiverDigest = copy.receiver.Digest().String()
		copy.args, err = copyArgs(id.args)
		if err != nil {
			return nil, err
		}
		copy.implicitInputs, err = copyArgs(id.implicitInputs)
		if err != nil {
			return nil, err
		}
		if id.module != nil {
			modID, err := copyID(id.module.id)
			if err != nil {
				return nil, err
			}
			copy.module = NewModule(modID, id.module.Name(), id.module.Ref(), id.module.Pin())
			copy.pb.Module = copy.module.pb
		}
		WithArgs(copy.args...)(copy)
		WithImplicitInputs(copy.implicitInputs...)(copy)
		marked := map[string]bool{}
		for _, extra := range id.pb.ExtraDigests {
			if extra.Label == ExtraDigestLabelRemoteCache {
				marked[extra.Digest] = true
			}
		}
		copy.pb.ExtraDigests = nil
		for _, extra := range id.pb.ExtraDigests {
			if marked[extra.Digest] && (extra.Label == ExtraDigestLabelRemoteCache || extra.Label == ExtraDigestLabelContent) {
				copy.pb.ExtraDigests = append(copy.pb.ExtraDigests, &callpbv1.ExtraDigest{Digest: extra.Digest, Label: extra.Label})
			}
		}
		copy.pb.Digest, err = copy.calcDigest()
		if err != nil {
			return nil, err
		}
		memo[id] = copy
		delete(active, id)
		return copy, nil
	}
	return copyID(id)
}
