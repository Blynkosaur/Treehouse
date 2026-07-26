//go:build race

package cmd

// The tests drive `th` as a subprocess, so `go test -race` instruments none of
// it unless the binary is built raced too — and ls now gathers its rows
// concurrently. This file exists only to tell TestMain which build to make.
func init() { raceBuild = true }
