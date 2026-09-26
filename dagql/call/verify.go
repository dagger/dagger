package call

import (
	"fmt"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// RecipeReferences returns every recipe dependency, including implicit inputs,
// module constructors and call literals nested in lists and input objects.
func RecipeReferences(c *callpbv1.Call) ([]string, error) {
	if c == nil || c.Type == nil {
		return nil, fmt.Errorf("call or type is missing")
	}
	var refs []string
	if c.ReceiverDigest != "" {
		refs = append(refs, c.ReceiverDigest)
	}
	if c.Module != nil {
		if c.Module.CallDigest == "" {
			return nil, fmt.Errorf("module recipe is missing")
		}
		refs = append(refs, c.Module.CallDigest)
	}
	var literal func(*callpbv1.Literal) error
	literal = func(l *callpbv1.Literal) error {
		if l == nil {
			return fmt.Errorf("literal is missing")
		}
		switch v := l.Value.(type) {
		case *callpbv1.Literal_CallDigest:
			if v.CallDigest == "" {
				return fmt.Errorf("literal call digest is missing")
			}
			refs = append(refs, v.CallDigest)
		case *callpbv1.Literal_List:
			if v.List == nil {
				return fmt.Errorf("list is missing")
			}
			for _, l := range v.List.Values {
				if err := literal(l); err != nil {
					return err
				}
			}
		case *callpbv1.Literal_Object:
			if v.Object == nil {
				return fmt.Errorf("object is missing")
			}
			for _, a := range v.Object.Values {
				if a == nil {
					return fmt.Errorf("object argument is missing")
				}
				if err := literal(a.Value); err != nil {
					return err
				}
			}
		case *callpbv1.Literal_DigestedString:
			if v.DigestedString == nil || v.DigestedString.Digest == "" {
				return fmt.Errorf("digested string identity is missing")
			}
		case *callpbv1.Literal_Null, *callpbv1.Literal_Bool, *callpbv1.Literal_Enum, *callpbv1.Literal_Int, *callpbv1.Literal_Float, *callpbv1.Literal_String_, *callpbv1.Literal_Bytes:
		default:
			return fmt.Errorf("unknown literal %T", v)
		}
		return nil
	}
	for _, args := range [][]*callpbv1.Argument{c.Args, c.ImplicitInputs} {
		for _, a := range args {
			if a == nil {
				return nil, fmt.Errorf("argument is missing")
			}
			if err := literal(a.Value); err != nil {
				return nil, err
			}
		}
	}
	return refs, nil
}
