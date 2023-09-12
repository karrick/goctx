//go:build !goctx_debug
// +build !goctx_debug

package goctx

// debug is a no-op for release builds
func debug(_ string, _ ...interface{}) {}
