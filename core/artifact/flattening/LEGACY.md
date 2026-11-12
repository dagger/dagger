# Legacy artifact selectors


`New`, `Plan.Flatten`, `Coordinates`, and `ArtifactDimensions` retain the legacy
type-path algorithm for existing schema and selector compatibility. The
following sections describe that algorithm.

### Try it

From the repository root:

```sh
go run ./core/artifact/flattening/cmd/flatten -legacy < core/artifact/flattening/testdata/compose.json
```

The input lists schema paths. Each path can list example items. An item supplies
one key per collection boundary, in parent-to-child order. A path with no items
still contributes dimensions and naming conflicts. It represents an empty
collection or a path that is outside the selected rows.

To display one path while retaining the complete schema for naming:

```sh
go run ./core/artifact/flattening/cmd/flatten \
  -legacy -only docker/compose/projects/services/up \
  < core/artifact/flattening/testdata/compose.json
```

The first output table shows dimensions and their names. The second shows full
coordinates for individual artifacts. These coordinates retain identity. Short
names are separate from identity values.

A dash marks an absent coordinate. Empty keys and control characters are quoted.
Every flattened row requires an artifact type. A row with a repeated dimension
returns an error instead of losing one of its coordinate values.

Run the package tests without an engine:

```sh
go test ./core/artifact/flattening
```

`ExamplePlan` in `flattening_test.go` shows a complete input and expected
output. Other tests show object fields, nested items, a module root collection,
main module shorthand, conflicts, and reserved CLI names.

`testdata/dagger-workspace.json` contains six paths and eight real Check rows
from `dagger/dagger` commit `7976c6f655aecf3d8de37326bfc09e01e666c98a`
(November 6, 2026). `dagger-workspace.txt` shows their complete coordinates,
including distinct `unit` and `slow` fields.

### Legacy input boundary

`Artifact.Path` is the public field path. A main module can omit its module
prefix. `Artifact.Module` retains the configured module name.

`Artifact.Collections` lists collection boundaries along that path. Each
`Collection.Fields` lists the ordinary object fields since the previous
collection item, or since the module root for the first collection. Synthetic
`get` operations are omitted.

For example, the route to a Compose service has two boundaries:

| Boundary | Fields since previous boundary | Example key |
| --- | --- | --- |
| Project item | `compose`, `projects` | `e2e` |
| Service item | `services` | `web` |

A function on the services collection has only the project boundary. It does
not have a service item coordinate. This distinction affects both its address
and the paths considered when shortening its type key.

Construct `Plan` from the complete schema. Then call `Flatten` for each
selected artifact. Filtering rows must not construct a smaller schema.

### Legacy rules

#### Dimensions and full coordinates

| Kind | Identifier | Coordinate |
| --- | --- | --- |
| Module | `module` | Configured module name |
| Collection item | `/module/field/...` | Discovered item key |
| Artifact type | `type:TypeName` | Full public field path, separated by `/` |

These three identifier forms are existing behavior. This extraction preserves
them so that they can be reviewed separately from the implementation.

A collection identifier starts at the module. Object fields extend that path.
A nested collection extends its parent collection's identifier. Field and module
names in collection identifiers use kebab case. The module coordinate retains
its configured spelling.

Collection dimensions occur in parent-to-child order in a row. A module
coordinate precedes them. A type coordinate follows them. The workspace's column
list is sorted by identifier.

A module root can itself be a collection. Its identifier is `/module-name`.
`module` and `/module` are distinct dimensions. An artifact without a workspace
module has no module dimension.

#### Dimension names

A collection's initial name comes from its item type. Candidate names add the
module and enough parent fields to resolve conflicts.

Parent collection fields can be omitted because their dimensions already supply
context. Ordinary object fields remain available as qualifiers. If those names
still conflict, parent collection names and the current collection field are
added as further qualifiers.

Resolution checks an exact identifier first. A qualified name has priority over
another dimension's alias. A collection alias has priority over a type alias.
An unknown name remains an unmatched selector. An ambiguous name returns an
error.

#### Type coordinate display

The full coordinate is always the field path. Without collection dimensions,
its display also retains that full path.

With collection dimensions, the display uses the shortest unique path suffix.
Candidates come from the complete schema and must have every supplied collection
dimension. They can have additional descendant dimensions. An empty collection
still contributes its schema paths.

If a full path is also another path's suffix, a leading `/` marks an exact path.
A shortened key is valid only with the collection dimensions used to choose it.

#### CLI names

CLI names are a separate allocation over the same dimension metadata. The CLI
supplies reserved names from peer commands. Key flags have priority over
collection presence flags. Both have priority over type flags.

Allocation orders dimensions by path depth, then identifier. It tries the
current candidate names before falling back to `dimension-...`. Presence
fallbacks add `-all`. Number suffixes resolve remaining reserved-name conflicts.
Names starting with `by-` are reserved for module selection.

Six type selectors are always included: Check, Generator, Service, Container,
Directory, and Expertise. Module aliases are allocated last. A module uses its
kebab-case name if that name is free and unique. Otherwise it uses `by-NAME`.

Flag allocation accepts a naming function. The CLI supplies its version-selected
naming rules. A nil function uses the current artifact naming rules. Collection
dimension creation also accepts a naming function. The CLI supplies its naming
rules. Engine and offline input use current artifact naming rules. Artifact
type names use current artifact naming rules.

The CLI still owns argument parsing, URL formatting, and proofs that a displayed
selector can be omitted. Those operations consume flattening metadata; they do
not assign dimension identities.

### Review boundary

The legacy `Plan` preserves the extracted type-path algorithm. Changes to that
heuristic are tracked in [#15125](https://github.com/dagger/dagger/issues/15125).
Use `NewSchema` for canonical module, collection, and name coordinates. See
[the package guide](README.md).

One current limitation is recorded in `TestCollectionItemIsAnotherCollection`.
If a collection item is itself a collection, there is no intervening field to
extend the identifier. Both boundaries get the same dimension identifier. The
coordinates contain two values for one column. This case needs a rule change
before full identity coverage can be claimed for all collection shapes.
