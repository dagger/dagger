package main

import "fmt"

// overwritten at build time
var gitCommit = "unknown"

const dnsnameVersion = "1.4.0-dev"

func getVersion() string {
	return fmt.Sprintf(`CNI dnsname plugin
version: %s
commit: %s`, dnsnameVersion, gitCommit)
}
