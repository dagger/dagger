package core

import _ "embed"

//go:embed private_key.pem
var globalPrivateKeyReadOnly string
