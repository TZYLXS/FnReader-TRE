//go:build windows

package main

import (
	"io/fs"
	"path/filepath"
)

func diskUsage(path string) (used, free int64, err error) {
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return nil
		}
		if info, ie := d.Info(); ie == nil {
			used += info.Size()
		}
		return nil
	})
	return used, 0, err
}
