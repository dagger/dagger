package cache

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckResponse(t *testing.T) {
	require.NoError(t, checkResponse(&http.Response{
		StatusCode: 200,
	}))
	require.NoError(t, checkResponse(&http.Response{
		StatusCode: 201,
	}))
	require.ErrorContains(t, checkResponse(&http.Response{
		StatusCode: 400,
		Body:       io.NopCloser(strings.NewReader("error message")),
	}), "error message")
	require.ErrorContains(t, checkResponse(&http.Response{
		StatusCode: 400,
		Body:       io.NopCloser(strings.NewReader("https://foo.com/bar")),
	}), "https://foo.com/*****")
	require.ErrorContains(t, checkResponse(&http.Response{
		StatusCode: 400,
		Body:       io.NopCloser(strings.NewReader("some other https://foo.com/bar error")),
	}), "some other https://foo.com/***** error")
}
