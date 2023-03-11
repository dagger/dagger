package core

// Void is returned by schema queries that have no return value.
type Void string

// Nothing is a canonical name for Void("").
var Nothing Void
