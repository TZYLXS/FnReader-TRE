//go:build windows

package main

// Windows 下只用于本机调试：没有 /proc/mounts，USB 自动检测不可用，
// 测试时在配置里直接指定 cache_dir。
func listMounts() []Mount { return nil }

func usbRoot() string { return "" }
