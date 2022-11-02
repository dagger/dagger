package handler_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"context"

	"github.com/dagger/dagger/router/internal/handler"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/gqlerrors"
	"github.com/dagger/graphql/language/location"
	"github.com/dagger/graphql/testutil"
)

const queryString = `query={name}`

func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder) *graphql.Result {
	// clone request body reader so that we can have a nicer error message
	bodyString := ""
	var target graphql.Result
	if b, err := io.ReadAll(recorder.Body); err == nil {
		bodyString = string(b)
	}
	readerClone := strings.NewReader(bodyString)

	decoder := json.NewDecoder(readerClone)
	err := decoder.Decode(&target)
	if err != nil {
		t.Fatalf("DecodeResponseToType(): %v \n%v", err.Error(), bodyString)
	}
	return &target
}
func executeTest(t *testing.T, h *handler.Handler, req *http.Request) (*graphql.Result, *httptest.ResponseRecorder) {
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	result := decodeResponse(t, resp)
	return result, resp
}

func TestContextPropagated(t *testing.T) {
	myNameQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"name": &graphql.Field{
				Name: "name",
				Type: graphql.String,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return p.Context.Value("name"), nil
				},
			},
		},
	})
	myNameSchema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: myNameQuery,
	})
	if err != nil {
		t.Fatal(err)
	}

	expected := &graphql.Result{
		Data: map[string]interface{}{
			"name": "context-data",
		},
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("/graphql?%v", queryString), nil)

	h := handler.New(&handler.Config{
		Schema: &myNameSchema,
		Pretty: true,
	})

	ctx := context.WithValue(context.Background(), "name", "context-data") //nolint
	resp := httptest.NewRecorder()
	h.ContextHandler(ctx, resp, req)
	result := decodeResponse(t, resp)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected server response %v", resp.Code)
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestHandler_BasicQuery_Pretty(t *testing.T) {
	expected := &graphql.Result{
		Data: map[string]interface{}{
			"hero": map[string]interface{}{
				"name": "R2-D2",
			},
		},
	}
	queryString := `query=query HeroNameQuery { hero { name } }&operationName=HeroNameQuery`
	req, _ := http.NewRequest("GET", fmt.Sprintf("/graphql?%v", queryString), nil)

	callbackCalled := false
	h := handler.New(&handler.Config{
		Schema: &testutil.StarWarsSchema,
		Pretty: true,
		ResultCallbackFn: func(ctx context.Context, params *graphql.Params, result *graphql.Result, responseBody []byte) {
			callbackCalled = true
			if params.OperationName != "HeroNameQuery" {
				t.Fatalf("OperationName passed to callback was not HeroNameQuery: %v", params.OperationName)
			}

			if result.HasErrors() {
				t.Fatalf("unexpected graphql result errors")
			}
		},
	})
	result, resp := executeTest(t, h, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected server response %v", resp.Code)
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
	if !callbackCalled {
		t.Fatalf("ResultCallbackFn was not called when it should have been")
	}
}

func TestHandler_BasicQuery_Ugly(t *testing.T) {
	expected := &graphql.Result{
		Data: map[string]interface{}{
			"hero": map[string]interface{}{
				"name": "R2-D2",
			},
		},
	}
	queryString := `query=query HeroNameQuery { hero { name } }`
	req, _ := http.NewRequest("GET", fmt.Sprintf("/graphql?%v", queryString), nil)

	h := handler.New(&handler.Config{
		Schema: &testutil.StarWarsSchema,
		Pretty: false,
	})
	result, resp := executeTest(t, h, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected server response %v", resp.Code)
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestHandler_Params_NilParams(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			if str, ok := r.(string); ok {
				if str != "undefined GraphQL schema" {
					t.Fatalf("unexpected error, got %v", r)
				}
				// test passed
				return
			}
			t.Fatalf("unexpected error, got %v", r)
		}
		t.Fatalf("expected to panic, did not panic")
	}()
	_ = handler.New(nil)
}

func TestHandler_BasicQuery_WithRootObjFn(t *testing.T) {
	myNameQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"name": &graphql.Field{
				Name: "name",
				Type: graphql.String,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					rv := p.Info.RootValue.(map[string]interface{})
					return rv["rootValue"], nil
				},
			},
		},
	})
	myNameSchema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: myNameQuery,
	})
	if err != nil {
		t.Fatal(err)
	}

	expected := &graphql.Result{
		Data: map[string]interface{}{
			"name": "foo",
		},
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("/graphql?%v", queryString), nil)

	h := handler.New(&handler.Config{
		Schema: &myNameSchema,
		Pretty: true,
		RootObjectFn: func(ctx context.Context, r *http.Request) map[string]interface{} {
			return map[string]interface{}{"rootValue": "foo"}
		},
	})
	result, resp := executeTest(t, h, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected server response %v", resp.Code)
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

type customError struct {
	message string
}

func (e customError) Error() string {
	return e.message
}

func TestHandler_BasicQuery_WithFormatErrorFn(t *testing.T) {
	resolverError := customError{message: "resolver error"}
	myNameQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"name": &graphql.Field{
				Name: "name",
				Type: graphql.String,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return nil, resolverError
				},
			},
		},
	})
	myNameSchema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: myNameQuery,
	})
	if err != nil {
		t.Fatal(err)
	}

	customFormattedError := gqlerrors.FormattedError{
		Message: resolverError.Error(),
		Locations: []location.SourceLocation{
			{
				Line:   1,
				Column: 2,
			},
		},
		Path: []interface{}{"name"},
	}

	expected := &graphql.Result{
		Data: map[string]interface{}{
			"name": nil,
		},
		Errors: []gqlerrors.FormattedError{customFormattedError},
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("/graphql?%v", queryString), nil)

	formatErrorFnCalled := false
	h := handler.New(&handler.Config{
		Schema: &myNameSchema,
		Pretty: true,
		FormatErrorFn: func(err error) gqlerrors.FormattedError {
			formatErrorFnCalled = true
			var formatted gqlerrors.FormattedError
			switch err := err.(type) {
			case *gqlerrors.Error:
				formatted = gqlerrors.FormatError(err)
			default:
				t.Fatalf("unexpected error type: %v", reflect.TypeOf(err))
			}
			return formatted
		},
	})
	result, resp := executeTest(t, h, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected server response %v", resp.Code)
	}
	if !formatErrorFnCalled {
		t.Fatalf("FormatErrorFn was not called when it should have been")
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
