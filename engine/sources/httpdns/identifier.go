package httpdns

import (
	bkhttp "github.com/moby/buildkit/source/http"
)

const AttrHTTPClientIDs = "dagger.http.clientids"

type HTTPIdentifier struct {
	bkhttp.HTTPIdentifier

	ClientIDs []string
}
