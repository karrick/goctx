//go:build goctx_debug
// +build goctx_debug

package goctx

import (
	"fmt"
	"os"
)

// debug formats and prints arguments to stderr for development builds
func debug(f string, a ...interface{}) {
	os.Stderr.Write([]byte("goctx: " + fmt.Sprintf(f, a...)))
}
