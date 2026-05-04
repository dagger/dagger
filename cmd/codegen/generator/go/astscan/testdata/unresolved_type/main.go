package main

// Scanner should fail with "unsupported external type foreign.Thing"
// prefixed by this file's path:line:col, so the module author can
// jump straight to the offending declaration.

import "example.com/foreign"

type Echo struct{}

func (e *Echo) Use(x foreign.Thing) string { return "" }
