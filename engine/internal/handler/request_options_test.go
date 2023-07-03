package handler

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/dagger/graphql/testutil"
)

const testQuery = "query RebelsShipsQuery { rebels { name } }"

func TestRequestOptions_GET_BasicQueryString(t *testing.T) {
	queryString := fmt.Sprintf("query=%s", testQuery)
	expected := &RequestOptions{
		Query:     testQuery,
		Variables: make(map[string]interface{}),
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("/graphql?%v", queryString), nil)
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_GET_ContentTypeApplicationGraphQL(t *testing.T) {
	body := []byte(testQuery)
	expected := &RequestOptions{}

	req, _ := http.NewRequest("GET", "/graphql", bytes.NewBuffer(body))
	req.Header.Add("Content-Type", "application/graphql")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_GET_ContentTypeApplicationJSON(t *testing.T) {
	body := fmt.Sprintf(`
	{
		"query": %q
	}`, testQuery)
	expected := &RequestOptions{}

	req, _ := http.NewRequest("GET", "/graphql", bytes.NewBufferString(body))
	req.Header.Add("Content-Type", "application/json")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_GET_ContentTypeApplicationUrlEncoded(t *testing.T) {
	data := url.Values{}
	data.Add("query", testQuery)

	expected := &RequestOptions{}

	req, _ := http.NewRequest("GET", "/graphql", bytes.NewBufferString(data.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestRequestOptions_POST_BasicQueryString_WithNoBody(t *testing.T) {
	queryString := fmt.Sprintf("query=%s", testQuery)
	expected := &RequestOptions{
		Query:     testQuery,
		Variables: make(map[string]interface{}),
	}

	req, _ := http.NewRequest("POST", fmt.Sprintf("/graphql?%v", queryString), nil)
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationGraphQL(t *testing.T) {
	body := []byte(testQuery)
	expected := &RequestOptions{
		Query: testQuery,
	}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBuffer(body))
	req.Header.Add("Content-Type", "application/graphql")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationGraphQL_WithNonGraphQLQueryContent(t *testing.T) {
	body := []byte(`not a graphql query`)
	expected := &RequestOptions{
		Query: "not a graphql query",
	}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBuffer(body))
	req.Header.Add("Content-Type", "application/graphql")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationGraphQL_EmptyBody(t *testing.T) {
	body := []byte(``)
	expected := &RequestOptions{
		Query: "",
	}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBuffer(body))
	req.Header.Add("Content-Type", "application/graphql")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationGraphQL_NilBody(t *testing.T) {
	expected := &RequestOptions{}

	req, _ := http.NewRequest("POST", "/graphql", nil)
	req.Header.Add("Content-Type", "application/graphql")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestRequestOptions_POST_ContentTypeApplicationJSON(t *testing.T) {
	body := fmt.Sprintf(`
	{
		"query": %q
	}`, testQuery)
	expected := &RequestOptions{
		Query: testQuery,
	}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBufferString(body))
	req.Header.Add("Content-Type", "application/json")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestRequestOptions_GET_WithVariablesAsObject(t *testing.T) {
	variables := url.QueryEscape(`{ "a": 1, "b": "2" }`)
	query := url.QueryEscape(testQuery)
	queryString := fmt.Sprintf("query=%s&variables=%s", query, variables)
	expected := &RequestOptions{
		Query: testQuery,
		Variables: map[string]interface{}{
			"a": float64(1),
			"b": "2",
		},
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("/graphql?%v", queryString), nil)
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestRequestOptions_POST_ContentTypeApplicationJSON_WithVariablesAsObject(t *testing.T) {
	body := fmt.Sprintf(`
	{
		"query": %q,
		"variables": { "a": 1, "b": "2" }
	}`, testQuery)
	expected := &RequestOptions{
		Query: testQuery,
		Variables: map[string]interface{}{
			"a": float64(1),
			"b": "2",
		},
	}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBufferString(body))
	req.Header.Add("Content-Type", "application/json")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationJSON_WithVariablesAsString(t *testing.T) {
	body := fmt.Sprintf(`
	{
		"query": %q,
		"variables": "{ \"a\": 1, \"b\": \"2\" }"
	}`, testQuery)
	expected := &RequestOptions{
		Query: testQuery,
		Variables: map[string]interface{}{
			"a": float64(1),
			"b": "2",
		},
	}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBufferString(body))
	req.Header.Add("Content-Type", "application/json")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationJSON_WithInvalidJSON(t *testing.T) {
	body := `INVALIDJSON{}`
	expected := &RequestOptions{}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBufferString(body))
	req.Header.Add("Content-Type", "application/json")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationJSON_WithNilBody(t *testing.T) {
	expected := &RequestOptions{}

	req, _ := http.NewRequest("POST", "/graphql", nil)
	req.Header.Add("Content-Type", "application/json")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestRequestOptions_POST_ContentTypeApplicationUrlEncoded(t *testing.T) {
	data := url.Values{}
	data.Add("query", testQuery)

	expected := &RequestOptions{
		Query:     testQuery,
		Variables: make(map[string]interface{}),
	}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBufferString(data.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationUrlEncoded_WithInvalidData(t *testing.T) {
	data := "Invalid Data"

	expected := &RequestOptions{}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBufferString(data))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_POST_ContentTypeApplicationUrlEncoded_WithNilBody(t *testing.T) {
	expected := &RequestOptions{}

	req, _ := http.NewRequest("POST", "/graphql", nil)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestRequestOptions_PUT_BasicQueryString(t *testing.T) {
	queryString := fmt.Sprintf("query=%s", testQuery)
	expected := &RequestOptions{
		Query:     testQuery,
		Variables: make(map[string]interface{}),
	}

	req, _ := http.NewRequest("PUT", fmt.Sprintf("/graphql?%v", queryString), nil)
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_PUT_ContentTypeApplicationGraphQL(t *testing.T) {
	body := []byte(testQuery)
	expected := &RequestOptions{}

	req, _ := http.NewRequest("PUT", "/graphql", bytes.NewBuffer(body))
	req.Header.Add("Content-Type", "application/graphql")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_PUT_ContentTypeApplicationJSON(t *testing.T) {
	body := fmt.Sprintf(`
	{
		"query": %q
	}`, testQuery)
	expected := &RequestOptions{}

	req, _ := http.NewRequest("PUT", "/graphql", bytes.NewBufferString(body))
	req.Header.Add("Content-Type", "application/json")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_PUT_ContentTypeApplicationUrlEncoded(t *testing.T) {
	data := url.Values{}
	data.Add("query", testQuery)

	expected := &RequestOptions{}

	req, _ := http.NewRequest("PUT", "/graphql", bytes.NewBufferString(data.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestRequestOptions_DELETE_BasicQueryString(t *testing.T) {
	queryString := fmt.Sprintf("query=%s", testQuery)
	expected := &RequestOptions{
		Query:     testQuery,
		Variables: make(map[string]interface{}),
	}

	req, _ := http.NewRequest("DELETE", fmt.Sprintf("/graphql?%v", queryString), nil)
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_DELETE_ContentTypeApplicationGraphQL(t *testing.T) {
	body := []byte(testQuery)
	expected := &RequestOptions{}

	req, _ := http.NewRequest("DELETE", "/graphql", bytes.NewBuffer(body))
	req.Header.Add("Content-Type", "application/graphql")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_DELETE_ContentTypeApplicationJSON(t *testing.T) {
	body := fmt.Sprintf(`
	{
		"query": %q
	}`, testQuery)
	expected := &RequestOptions{}

	req, _ := http.NewRequest("DELETE", "/graphql", bytes.NewBufferString(body))
	req.Header.Add("Content-Type", "application/json")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
func TestRequestOptions_DELETE_ContentTypeApplicationUrlEncoded(t *testing.T) {
	data := url.Values{}
	data.Add("query", testQuery)

	expected := &RequestOptions{}

	req, _ := http.NewRequest("DELETE", "/graphql", bytes.NewBufferString(data.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}

func TestRequestOptions_POST_UnsupportedContentType(t *testing.T) {
	body := `<xml>query{}</xml>`
	expected := &RequestOptions{}

	req, _ := http.NewRequest("POST", "/graphql", bytes.NewBufferString(body))
	req.Header.Add("Content-Type", "application/xml")
	result := NewRequestOptions(req)

	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("wrong result, graphql result diff: %v", testutil.Diff(expected, result))
	}
}
