package httpdns

import (
	bkhttp "github.com/moby/buildkit/source/http"
)

type HTTPIdentifier struct {
	bkhttp.HTTPIdentifier

	ClientIDs []string
}
