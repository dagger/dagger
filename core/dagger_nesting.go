package core

import (
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/vektah/gqlparser/v2/ast"
)

// DaggerNesting controls whether a process can connect back to Dagger and
// which session boundary that connection uses.
type DaggerNesting string

var DaggerNestings = dagql.NewEnum[DaggerNesting]()

var (
	DaggerNestingNestedClient = DaggerNestings.Register(
		"NESTED_CLIENT",
		"Connect to the session that created the process.",
	)
	DaggerNestingIndependentSessions = DaggerNestings.Register(
		"INDEPENDENT_SESSIONS",
		"Allow the process to create independent ordinary sessions.",
	)
)

func (DaggerNesting) Type() *ast.Type {
	return &ast.Type{NamedType: "DaggerNesting", NonNull: true}
}

func (DaggerNesting) TypeDescription() string {
	return "How a process may connect back to Dagger."
}

func (DaggerNesting) Decoder() dagql.InputDecoder {
	return DaggerNestings
}

func (mode DaggerNesting) ToLiteral() call.Literal {
	return DaggerNestings.Literal(mode)
}

// ValidateDaggerNesting rejects conflicting legacy and enum inputs.
func ValidateDaggerNesting(legacy bool, nesting dagql.Optional[DaggerNesting]) error {
	_, err := daggerNestingMode(legacy, nesting)
	return err
}

// daggerNestingMode validates the public compatibility inputs and returns the
// executor listener mode. The legacy flag deliberately maps to None: existing
// nested metadata still selects the legacy nested handler, while the absence of
// an explicit mode keeps DAGGER_NESTING absent byte-for-byte.
func daggerNestingMode(legacy bool, nesting dagql.Optional[DaggerNesting]) (engineutil.DaggerNestingMode, error) {
	if legacy && nesting.Valid {
		return engineutil.DaggerNestingNone, fmt.Errorf("experimentalPrivilegedNesting cannot be combined with daggerNesting")
	}
	if !nesting.Valid {
		return engineutil.DaggerNestingNone, nil
	}

	switch nesting.Value {
	case DaggerNestingNestedClient:
		return engineutil.DaggerNestingNestedClient, nil
	case DaggerNestingIndependentSessions:
		return engineutil.DaggerNestingIndependentSessions, nil
	default:
		return engineutil.DaggerNestingNone, fmt.Errorf("unsupported daggerNesting value %q", nesting.Value)
	}
}
