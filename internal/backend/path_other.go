//go:build !windows

package backend

import "path/filepath"

func realPath(path string) (string, error) { return filepath.EvalSymlinks(path) }
