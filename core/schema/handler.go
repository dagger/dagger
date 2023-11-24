package schema

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/gqlerrors"
	"github.com/moby/buildkit/util/bklog"
)

const (
	ContentTypeJSON           = "application/json"
	ContentTypeGraphQL        = "application/graphql"
	ContentTypeFormURLEncoded = "application/x-www-form-urlencoded"
)

type ResultCallbackFn func(ctx context.Context, params *graphql.Params, result *graphql.Result, responseBody []byte)

type Handler struct {
	Schema           *graphql.Schema
	pretty           bool
	rootObjectFn     RootObjectFn
	resultCallbackFn ResultCallbackFn
	formatErrorFn    func(err error) gqlerrors.FormattedError
}

type RequestOptions struct {
	Query         string                 `json:"query" url:"query" schema:"query"`
	Variables     map[string]interface{} `json:"variables" url:"variables" schema:"variables"`
	OperationName string                 `json:"operationName" url:"operationName" schema:"operationName"`
}

// a workaround for getting`variables` as a JSON string
type requestOptionsCompatibility struct {
	Query         string `json:"query" url:"query" schema:"query"`
	Variables     string `json:"variables" url:"variables" schema:"variables"`
	OperationName string `json:"operationName" url:"operationName" schema:"operationName"`
}

func getFromForm(values url.Values) *RequestOptions {
	query := values.Get("query")
	if query != "" {
		// get variables map
		variables := make(map[string]interface{}, len(values))
		variablesStr := values.Get("variables")
		json.Unmarshal([]byte(variablesStr), &variables)

		return &RequestOptions{
			Query:         query,
			Variables:     variables,
			OperationName: values.Get("operationName"),
		}
	}

	return nil
}

// RequestOptions Parses a http.Request into GraphQL request options struct
func NewRequestOptions(r *http.Request) *RequestOptions {
	if reqOpt := getFromForm(r.URL.Query()); reqOpt != nil {
		return reqOpt
	}

	if r.Method != http.MethodPost {
		return &RequestOptions{}
	}

	if r.Body == nil {
		return &RequestOptions{}
	}

	// TODO: improve Content-Type handling
	contentTypeStr := r.Header.Get("Content-Type")
	contentTypeTokens := strings.Split(contentTypeStr, ";")
	contentType := contentTypeTokens[0]

	switch contentType {
	case ContentTypeGraphQL:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return &RequestOptions{}
		}
		return &RequestOptions{
			Query: string(body),
		}
	case ContentTypeFormURLEncoded:
		if err := r.ParseForm(); err != nil {
			return &RequestOptions{}
		}

		if reqOpt := getFromForm(r.PostForm); reqOpt != nil {
			return reqOpt
		}

		return &RequestOptions{}

	case ContentTypeJSON:
		fallthrough
	default:
		var opts RequestOptions
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return &opts
		}
		err = json.Unmarshal(body, &opts)
		if err != nil {
			// Probably `variables` was sent as a string instead of an object.
			// So, we try to be polite and try to parse that as a JSON string
			var optsCompatible requestOptionsCompatibility
			json.Unmarshal(body, &optsCompatible)
			json.Unmarshal([]byte(optsCompatible.Variables), &opts.Variables)
		}
		return &opts
	}
}

// ContextHandler provides an entrypoint into executing graphQL queries with a
// user-provided context.
func (h *Handler) ContextHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// this response may be tunneled through grpc, in which case it may need chunking in order
	// to not exceed the max grpc message size
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// get query
	opts := NewRequestOptions(r)

	// execute graphql query
	params := graphql.Params{
		Schema:         *h.Schema,
		RequestString:  opts.Query,
		VariableValues: opts.Variables,
		OperationName:  opts.OperationName,
		Context:        ctx,
	}
	if h.rootObjectFn != nil {
		params.RootObject = h.rootObjectFn(ctx, r)
	}

	result := graphql.Do(params)

	if formatErrorFn := h.formatErrorFn; formatErrorFn != nil && len(result.Errors) > 0 {
		formatted := make([]gqlerrors.FormattedError, len(result.Errors))
		for i, formattedError := range result.Errors {
			formatted[i] = formatErrorFn(formattedError.OriginalError())
		}
		result.Errors = formatted
	}
	for _, err := range result.Errors {
		bklog.G(ctx).WithError(err.OriginalError()).Debug("error while executing query")
	}

	// use proper JSON Header
	w.Header().Add("Content-Type", "application/json; charset=utf-8")

	var buff []byte
	var err error
	if h.pretty {
		w.WriteHeader(http.StatusOK)
		buff, err = json.MarshalIndent(result, "", "\t")
	} else {
		w.WriteHeader(http.StatusOK)
		buff, err = json.Marshal(result)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for len(buff) > buildkit.MaxFileContentsChunkSize {
		chunk := buff[:buildkit.MaxFileContentsChunkSize]
		buff = buff[buildkit.MaxFileContentsChunkSize:]
		w.Write(chunk)
		flusher.Flush()
	}
	if len(buff) > 0 {
		w.Write(buff)
	}

	if h.resultCallbackFn != nil {
		h.resultCallbackFn(ctx, &params, result, buff)
	}
}

// ServeHTTP provides an entrypoint into executing graphQL queries.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// NOTE: putting this here because we can at this point be relatively sure this is a normal
	// graphql http request, as opposed to a request that's gonna get hijacked, in which case
	// writing headers can break stuff
	w.Header().Add(engine.EngineVersionMetaKey, engine.Version)
	h.ContextHandler(r.Context(), w, r)
}

// RootObjectFn allows a user to generate a RootObject per request
type RootObjectFn func(ctx context.Context, r *http.Request) map[string]interface{}

type HandlerConfig struct {
	Schema           *graphql.Schema
	Pretty           bool
	RootObjectFn     RootObjectFn
	ResultCallbackFn ResultCallbackFn
	FormatErrorFn    func(err error) gqlerrors.FormattedError
}

func NewConfig() *HandlerConfig {
	return &HandlerConfig{
		Schema: nil,
		Pretty: true,
	}
}

func NewHandler(p *HandlerConfig) *Handler {
	if p == nil {
		p = NewConfig()
	}

	if p.Schema == nil {
		panic("undefined GraphQL schema")
	}

	return &Handler{
		Schema:           p.Schema,
		pretty:           p.Pretty,
		rootObjectFn:     p.RootObjectFn,
		resultCallbackFn: p.ResultCallbackFn,
		formatErrorFn:    p.FormatErrorFn,
	}
}
