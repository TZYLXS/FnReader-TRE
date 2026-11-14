//go:build !windows

package main

import "syscall"

func diskUsage(path string) (used, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := int64(st.Bsize)
	total := int64(st.Blocks) * bsize
	free = int64(st.Bavail) * bsize
	used = total - int64(st.Bfree)*bsize
	return used, free, nil
}
