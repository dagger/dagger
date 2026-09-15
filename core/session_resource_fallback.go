package core

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func isRetryableSessionAttachableErr(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	switch status.Code(err) {
	case codes.Canceled, codes.Unavailable:
		return true
	default:
		return false
	}
}
