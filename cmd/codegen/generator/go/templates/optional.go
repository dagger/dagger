package templates

import (
	"go/types"
	"strings"
)

func isOptionalGoType(typ types.Type) bool {
	switch t := typ.(type) {
	case *types.Pointer:
		return true
	case *types.Named:
		if pkg := t.Obj().Pkg(); pkg != nil {
			if strings.HasSuffix(pkg.Path(), "/dagql") {
				switch t.Obj().Name() {
				case "Optional", "Nullable":
					return true
				}
			}
		}
		return isOptionalGoType(t.Underlying())
	default:
		return false
	}
}
