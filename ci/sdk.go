package main

type SDK struct {
	Go *GoSDK
}

// type SDK interface {
// 	Lint(ctx context.Context) error
// 	Test(ctx context.Context) error
// 	Generate(ctx context.Context) (*Directory, error)
// 	Publish(ctx context.Context, tag string) error
// 	Bump(ctx context.Context, engineVersion string) error
// }
