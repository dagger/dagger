package dagger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/executor"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func Serve(ctx context.Context, schema graphql.ExecutableSchema) {
	ctx = WithUnixSocketAPIClient(ctx, "/dagger.sock")
	ctx = graphql.StartOperationTrace(ctx)

	input, err := os.Open("/inputs/dagger.json")
	if err != nil {
		writeErrorf("unable to open request file: %v", err)
	}

	var params *graphql.RawParams
	dec := json.NewDecoder(input)
	dec.UseNumber()
	start := graphql.Now()
	if err := dec.Decode(&params); err != nil {
		writeErrorf("json body could not be decoded: %v", err)
		return
	}
	params.ReadTime = graphql.TraceTiming{
		Start: start,
		End:   graphql.Now(),
	}

	exec := executor.New(schema)
	rc, ocErr := exec.CreateOperationContext(ctx, params)
	if err != nil {
		resp := exec.DispatchError(graphql.WithOperationContext(ctx, rc), ocErr)
		writeResponse(resp)
		return
	}
	responses, ctx := exec.DispatchOperation(ctx, rc)
	writeResponse(responses(ctx))
}

func writeResponse(response *graphql.Response) {
	if response.Errors != nil {
		fmt.Printf("%v\n", response.Errors)
		os.Exit(1)
	}

	output, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile("/outputs/dagger.json", output, 0644); err != nil {
		panic(err)
	}
}

func writeErrorf(format string, args ...interface{}) {
	writeResponse(&graphql.Response{Errors: gqlerror.List{{Message: fmt.Sprintf(format, args...)}}})
}
