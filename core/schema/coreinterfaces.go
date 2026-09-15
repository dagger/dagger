package schema

import "github.com/dagger/dagger/dagql"

// installCoreInterfaces registers Dagger-specific interfaces. Like all dagql
// interfaces, these are matched structurally: any object whose fields line up
// automatically declares conformance, with no explicit implements declaration.
//
// Unlike Node (which lives in dagql and is pure GraphQL infrastructure),
// these interfaces encode Dagger-specific semantics.
func installCoreInterfaces(srv *dagql.Server) {
	syncer := dagql.NewInterface("Syncer", dagql.FormatDescription(
		`An object that can be force-evaluated.`,
		`Calling sync ensures that the object's entire dependency DAG has been evaluated, returning the object's ID once complete.`,
	))
	syncer.AddField(dagql.InterfaceFieldSpec{
		FieldSpec: dagql.FieldSpec{
			Name: "id",
			Type: dagql.AnyID{},
		},
	})
	syncer.AddField(dagql.InterfaceFieldSpec{
		FieldSpec: dagql.FieldSpec{
			Name: "sync",
			Type: dagql.AnyID{},
		},
	})
	srv.InstallInterface(syncer)

	exportable := dagql.NewInterface("Exportable", dagql.FormatDescription(
		`An object that can be exported to the host.`,
		`Calling export writes the object to a path on the host filesystem and returns the path that was written.`,
	))
	exportable.AddField(dagql.InterfaceFieldSpec{
		FieldSpec: dagql.FieldSpec{
			Name: "id",
			Type: dagql.AnyID{},
		},
	})
	exportable.AddField(dagql.InterfaceFieldSpec{
		FieldSpec: dagql.FieldSpec{
			Name: "export",
			Type: dagql.String(""),
			Args: dagql.NewInputSpecs(
				dagql.InputSpec{Name: "path", Type: dagql.String("")},
			),
		},
	})
	srv.InstallInterface(exportable)
}
