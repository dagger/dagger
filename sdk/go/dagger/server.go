package dagger

import (
	"context"
	"encoding/json"
	"os"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/executor"
)

func Serve(ctx context.Context, schema graphql.ExecutableSchema) {
	ctx = WithUnixSocketAPIClient(ctx, "/dagger.sock")
	ctx = graphql.StartOperationTrace(ctx)

	input, err := os.Open("/inputs/dagger.json")
	if err != nil {
		panic(err)
	}

	var params *graphql.RawParams
	dec := json.NewDecoder(input)
	dec.UseNumber()
	start := graphql.Now()
	if err := dec.Decode(&params); err != nil {
		panic(err)
		// 	// w.WriteHeader(http.StatusBadRequest)
		// 	// writeJsonErrorf(w, "json body could not be decoded: "+err.Error())
		// 	// return
	}
	params.ReadTime = graphql.TraceTiming{
		Start: start,
		End:   graphql.Now(),
	}

	exec := executor.New(schema)
	rc, ocErr := exec.CreateOperationContext(ctx, params)
	if err != nil {
		// w.WriteHeader(statusFor(err))
		resp := exec.DispatchError(graphql.WithOperationContext(ctx, rc), ocErr)
		writeResponse(resp)
		return
	}
	responses, ctx := exec.DispatchOperation(ctx, rc)
	writeResponse(responses(ctx))
}

func writeResponse(response *graphql.Response) {
	output, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile("/outputs/dagger.json", output, 0644); err != nil {
		panic(err)
	}
}
