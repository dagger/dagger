package main

import (
	"io/fs"
	"path/filepath"

	"dagger.io/dagger"
)

func main() {
	dagger.ServeCommands(
		TestFile,
		TestDir,
		TestImportedProjectDir,
	)
}

func TestFile(ctx dagger.Context, prefix string) (*dagger.File, error) {
	name := prefix + "foo.txt"
	return ctx.Client().Directory().
		WithNewFile(name, "foo\n").
		File(name), nil
}

func TestDir(ctx dagger.Context, prefix string) (*dagger.Directory, error) {
	return ctx.Client().Directory().
		WithNewDirectory(prefix+"subdir").
		WithNewFile(prefix+"subdir/subbar1.txt", "subbar1\n").
		WithNewFile(prefix+"subdir/subbar2.txt", "subbar2\n").
		WithNewFile(prefix+"bar1.txt", "bar1\n").
		WithNewFile(prefix+"bar2.txt", "bar2\n"), nil
}

func TestImportedProjectDir(ctx dagger.Context) (string, error) {
	var output string
	err := filepath.Walk(".", func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		output += path + "\n"
		return nil
	})
	return output, err
}
