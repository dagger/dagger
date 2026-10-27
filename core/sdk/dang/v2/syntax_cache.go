package dangv2

import (
	"context"

	"github.com/vito/dang/v2/pkg/dang"
)

// Share parsed syntax only. Dang copies it before inference and evaluation,
// keeping clients, schemas and values separate for each call. The runtime
// limits the cache to 128 entries and an estimated 64 MiB.
var moduleSyntaxCache = dang.NewSyntaxCache()

func withDangSyntaxCache(ctx context.Context) context.Context {
	return dang.WithSyntaxCache(ctx, moduleSyntaxCache)
}
