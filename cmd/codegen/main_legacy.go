//go:build legacy_typedefs
// +build legacy_typedefs

package main

func init() {
	rootCmd.AddCommand(generateTypeDefsCmd)
}
