package dangv2

import (
	"context"
	"errors"

	"github.com/dagger/dagger/core"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/dang/v2/pkg/dang"
)

// convertError converts an error from Dang evaluation into a *core.Error, as
// dangshared.ConvertError does. A GraphQL error that Dang code rescued and
// raised again as it was (`x rescue { e: GraphQLError! => raise e }`) is
// converted as it would have been had nothing rescued it, extensions and all.
func convertError(err error) *core.Error {
	var raised *dang.RaisedError
	if errors.As(err, &raised) {
		if origin := raised.Origin(); origin != nil && errors.As(origin, new(*gqlerror.Error)) {
			err = origin
		}
	}
	return dangshared.ConvertError(err)
}

// returnRaisedError makes a raise of a Dagger Error, the failure err of
// fnCall's Dang code, the error fnCall returns: the engine then hands its
// caller that Error, values and all, as it does the one a module process
// returns with FunctionCall.returnError, not a new one with its message. An
// SDK entrypoint can so pass on the Error its module process built, loaded
// by ID. Any other failure is left to convertError. It returns err.
//
// It must run while the nested client that evaluated the code still serves:
// reading the Error's ID can take a query.
func returnRaisedError(ctx context.Context, fnCall *core.FunctionCall, err error) error {
	var raised *dang.RaisedError
	if !errors.As(err, &raised) {
		return err
	}
	value, ok := raised.Value.(dang.GraphQLValue)
	if !ok || value.TypeName != (&core.Error{}).Type().Name() {
		return err
	}
	encoded, idErr := value.ID(ctx)
	if idErr != nil {
		// The raise already read the Error's message, which says why it
		// failed to load, if it did.
		return err
	}
	var id dagql.ID[*core.Error]
	if id.Decode(encoded) != nil {
		return err
	}
	_ = fnCall.ReturnError(ctx, id)
	return err
}
