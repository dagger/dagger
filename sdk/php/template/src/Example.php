<?php declare(strict_types=1);

namespace DaggerModule;

class Example
{
// // Returns a container that echoes whatever string argument is provided
// func (m *PhpSdk) ContainerEcho(stringArg string) *Container {
//	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
// }
//
// // Returns lines that match a pattern in the files of the provided Directory
// func (m *PhpSdk) GrepDir(ctx context.Context, directoryArg *Directory, pattern string) (string, error) {
//	return dag.Container().
//		From("alpine:latest").
//		WithMountedDirectory("/mnt", directoryArg).
//		WithWorkdir("/mnt").
//		WithExec([]string{"grep", "-R", pattern, "."}).
//		Stdout(ctx)
// }
}