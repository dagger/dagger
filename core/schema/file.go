package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

type fileSchema struct{}

var _ SchemaResolvers = &fileSchema{}

func (s *fileSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("file", s.file).
			Doc(`Creates a file with the specified contents.`).
			Args(
				dagql.Arg("name").Doc(`Name of the new file. Example: "foo.txt"`),
				dagql.Arg("contents").Doc(`Contents of the new file. Example: "Hello world!"`),
				dagql.Arg("permissions").Doc(`Permissions of the new file. Example: 0600`),
			),
	}.Install(srv)

	dagql.Fields[*core.File]{
		Syncer[*core.File]().
			Doc(`Force evaluation in the engine.`),
		dagql.NodeFunc("contents", s.contents).
			Doc(`Retrieves the contents of the file.`).
			Args(
				dagql.Arg("offsetLines").Doc(`Start reading after this line`),
				dagql.Arg("limitLines").Doc(`Maximum number of lines to read`),
			),
		dagql.NodeFunc("size", s.size).
			Doc(`Retrieves the size of the file, in bytes.`),
		dagql.NodeFunc("name", s.name).
			Doc(`Retrieves the name of the file.`),
		dagql.NodeFunc("stat", s.stat).
			Doc(`Return file status`),
		dagql.NodeFunc("digest", s.digest).
			Doc(
				`Return the file's digest.
				The format of the digest is not guaranteed to be stable between releases of Dagger.
				It is guaranteed to be stable between invocations of the same Dagger engine.`,
			).
			Args(
				dagql.Arg("excludeMetadata").Doc(`If true, exclude metadata from the digest.`),
			),
		dagql.NodeFunc("withName", s.withName).
			IsPersistable().
			Doc(`Retrieves this file with its name set to the given name.`).
			Args(
				dagql.Arg("name").Doc(`Name to set file to.`),
			),
		dagql.NodeFunc("search", s.search).
			IsPersistable().
			Doc(
				// NOTE: sync with Directory.search
				`Searches for content matching the given regular expression or literal string.`,
				`Uses Rust regex syntax; escape literal ., [, ], {, }, | with backslashes.`,
			).
			Args((core.SearchOpts{}).Args()...),
		dagql.NodeFunc("withReplaced", s.withReplaced).
			IsPersistable().
			Doc(
				`Retrieves the file with content replaced with the given text.`,
				`If 'all' is true, all occurrences of the pattern will be replaced.`,
				`If 'firstAfter' is specified, only the first match starting at the specified line will be replaced.`,
				`If neither are specified, and there are multiple matches for the pattern, this will error.`,
				`If there are no matches for the pattern, this will error.`,
			).
			Args(
				dagql.Arg("search").Doc(`The text to match.`),
				dagql.Arg("replacement").Doc(`The text to match.`),
				dagql.Arg("all").Doc(`Replace all occurrences of the pattern.`),
				dagql.Arg("firstFrom").Doc(`Replace the first match starting from the specified line.`),
			),
		dagql.NodeFunc("export", s.export).
			WithInput(dagql.PerClientInput).
			View(AllVersion).
			DoNotCache("Writes to the local host.").
			Doc(`Writes the file to a file path on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the written directory (e.g., "output.txt").`),
				dagql.Arg("allowParentDirPath").Doc(
					`If allowParentDirPath is true, the path argument can be a directory
				path, in which case the file will be created in that directory.`),
			),
		dagql.NodeFunc("export", s.exportLegacy).
			WithInput(dagql.PerClientInput).
			View(BeforeVersion("v0.12.0")).
			Extend(),
		dagql.NodeFunc("withTimestamps", s.withTimestamps).
			IsPersistable().
			Doc(`Retrieves this file with its created/modified timestamps set to the given time.`).
			Args(
				dagql.Arg("timestamp").Doc(`Timestamp to set dir/files in.`,
					`Formatted in seconds following Unix epoch (e.g., 1672531199).`),
			),
		dagql.NodeFunc("chown", s.chown).
			IsPersistable().
			Doc(`Change the owner of the file recursively.`).
			Args(
				dagql.Arg("owner").Doc(`A user:group to set for the file.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
			),
		dagql.NodeFunc("asJSON", s.asJSON).
			Doc(`Parse the file contents as JSON.`),
	}.Install(srv)
}

type newFileArgs struct {
	Name        string
	Contents    string
	Permissions int `default:"0644"`
}

func (s *fileSchema) file(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args newFileArgs,
) (inst dagql.ObjectResult[*core.File], err error) {
	if dir, _ := filepath.Split(args.Name); dir != "" {
		return inst, fmt.Errorf("file name %q must not contain a directory", args.Name)
	}
	if err := core.ValidateFileName(args.Name); err != nil {
		return inst, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	err = srv.Select(ctx, parent, &inst,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(args.Name)},
			{Name: "contents", Value: dagql.String(args.Contents)},
			{Name: "permissions", Value: dagql.Int(args.Permissions)},
		}},
		dagql.Selector{Field: "file", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(args.Name)},
		}},
	)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *fileSchema) contents(ctx context.Context, file dagql.ObjectResult[*core.File], args struct {
	OffsetLines *int
	LimitLines  *int
}) (dagql.String, error) {
	content, err := file.Self().Contents(ctx, file, args.OffsetLines, args.LimitLines)
	if err != nil {
		return "", err
	}

	return dagql.NewString(string(content)), nil
}

func (s *fileSchema) size(ctx context.Context, file dagql.ObjectResult[*core.File], args struct{}) (dagql.Int, error) {
	info, err := file.Self().Stat(ctx, file)
	if err != nil {
		return 0, err
	}

	return dagql.NewInt(info.Size), nil
}

func (s *fileSchema) name(ctx context.Context, file dagql.ObjectResult[*core.File], args struct{}) (dagql.String, error) {
	filePath, err := file.Self().File.GetOrEval(ctx, file.Result)
	if err != nil {
		return "", err
	}
	return dagql.NewString(filepath.Base(filePath)), nil
}

func (s *fileSchema) stat(ctx context.Context, parent dagql.ObjectResult[*core.File], args struct{}) (*core.Stat, error) {
	return parent.Self().Stat(ctx, parent)
}

type fileDigestArgs struct {
	ExcludeMetadata bool `default:"false"`
}

func (s *fileSchema) digest(ctx context.Context, file dagql.ObjectResult[*core.File], args fileDigestArgs) (dagql.String, error) {
	digest, err := file.Self().Digest(ctx, file, args.ExcludeMetadata)
	if err != nil {
		return "", err
	}

	return dagql.NewString(digest), nil
}

type fileWithNameArgs struct {
	Name string
}

func (s *fileSchema) withName(ctx context.Context, parent dagql.ObjectResult[*core.File], args fileWithNameArgs) (inst dagql.ObjectResult[*core.File], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if dir, _ := filepath.Split(args.Name); dir != "" {
		return inst, fmt.Errorf("file name %q must not contain a directory", args.Name)
	}

	file := &core.File{
		Platform: parent.Self().Platform,
		Services: slices.Clone(parent.Self().Services),
		Lazy: &core.FileWithNameLazy{
			LazyState: core.NewLazyState(),
			Parent:    parent,
			Filename:  args.Name,
		},
		File:     new(core.LazyAccessor[string, *core.File]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]),
	}
	if parentPath, ok := parent.Self().File.Peek(); ok {
		file.File.SetValue(filepath.Join(filepath.Dir(parentPath), args.Name))
	}

	return dagql.NewObjectResultForCurrentCall(ctx, srv, file)
}

type fileExportArgs struct {
	Path               string
	AllowParentDirPath bool `default:"false"`
}

func (s *fileSchema) search(ctx context.Context, parent dagql.ObjectResult[*core.File], args searchArgs) (dagql.Array[*core.SearchResult], error) {
	return parent.Self().Search(ctx, parent, args.SearchOpts, true)
}

type fileReplaceArgs struct {
	Search      string
	Replacement string
	All         bool `default:"false"`
	FirstFrom   *int
}

func (s *fileSchema) withReplaced(ctx context.Context, parent dagql.ObjectResult[*core.File], args fileReplaceArgs) (inst dagql.ObjectResult[*core.File], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	file := &core.File{
		Platform: parent.Self().Platform,
		Services: slices.Clone(parent.Self().Services),
		Lazy: &core.FileWithReplacedLazy{
			LazyState:   core.NewLazyState(),
			Parent:      parent,
			Search:      args.Search,
			Replacement: args.Replacement,
			FirstFrom:   args.FirstFrom,
			All:         args.All,
		},
		File:     new(core.LazyAccessor[string, *core.File]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]),
	}
	if parentPath, ok := parent.Self().File.Peek(); ok {
		file.File.SetValue(parentPath)
	}

	return dagql.NewObjectResultForCurrentCall(ctx, srv, file)
}

func (s *fileSchema) export(ctx context.Context, parent dagql.ObjectResult[*core.File], args fileExportArgs) (dagql.String, error) {
	filePath, err := parent.Self().File.GetOrEval(ctx, parent.Result)
	if err != nil {
		return "", err
	}
	snapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return "", fmt.Errorf("failed to evaluate file: %w", err)
	}
	err = core.ExportFile(ctx, snapshot, filePath, args.Path, args.AllowParentDirPath)
	if err != nil {
		return "", err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get engine client: %w", err)
	}
	stat, err := bk.StatCallerHostPath(ctx, args.Path, true)
	if err != nil {
		return "", err
	}
	return dagql.String(stat.Path), err
}

func (s *fileSchema) exportLegacy(ctx context.Context, parent dagql.ObjectResult[*core.File], args fileExportArgs) (dagql.Boolean, error) {
	_, err := s.export(ctx, parent, args)
	if err != nil {
		return false, err
	}
	return true, nil
}

type fileWithTimestampsArgs struct {
	Timestamp int
}

func (s *fileSchema) withTimestamps(ctx context.Context, parent dagql.ObjectResult[*core.File], args fileWithTimestampsArgs) (inst dagql.ObjectResult[*core.File], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get Dagger server: %w", err)
	}

	f := &core.File{
		Platform: parent.Self().Platform,
		Services: slices.Clone(parent.Self().Services),
		Lazy: &core.FileWithTimestampsLazy{
			LazyState: core.NewLazyState(),
			Parent:    parent,
			Timestamp: args.Timestamp,
		},
		File:     new(core.LazyAccessor[string, *core.File]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]),
	}
	if parentPath, ok := parent.Self().File.Peek(); ok {
		f.File.SetValue(parentPath)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, f)
}

type fileChownArgs struct {
	Owner string
}

func (s *fileSchema) chown(
	ctx context.Context,
	parent dagql.ObjectResult[*core.File],
	args fileChownArgs,
) (inst dagql.ObjectResult[*core.File], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	f := &core.File{
		Platform: parent.Self().Platform,
		Services: slices.Clone(parent.Self().Services),
		Lazy: &core.FileChownLazy{
			LazyState: core.NewLazyState(),
			Parent:    parent,
			Owner:     args.Owner,
		},
		File:     new(core.LazyAccessor[string, *core.File]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]),
	}
	if parentPath, ok := parent.Self().File.Peek(); ok {
		f.File.SetValue(parentPath)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, f)
}

func (s *fileSchema) asJSON(ctx context.Context, parent dagql.ObjectResult[*core.File], args struct{}) (*core.JSONValue, error) {
	json, err := parent.Self().AsJSON(ctx, parent)
	if err != nil {
		return nil, err
	}
	return &core.JSONValue{Data: []byte(json)}, nil
}
