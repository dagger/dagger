package knowledge

import _ "embed"

//go:embed sdk/go.md
var GoSDK string

//go:embed querying.md
var Querying string

//go:embed shell.md
var Shell string
