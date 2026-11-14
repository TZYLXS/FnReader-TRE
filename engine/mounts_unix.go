//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var mountBlacklist = map[string]bool{
	"/": true, "/tmp": true, "/overlay": true, "/rom": true, "/boot": true,
	"/dev": true, "/proc": true, "/sys": true, "/usr": true, "/var": true,
	"/etc": true, "/home": true, "/root": true, "/mnt": true, "/media": true,
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

func listMounts() []Mount {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	out := make([]Mount, 0, 16)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		dev := unescapeMount(fields[0])
		mp := unescapeMount(fields[1])
		fs := fields[2]
		if fs == "tmpfs" || fs == "devtmpfs" || fs == "proc" || fs == "sysfs" || fs == "overlay" || fs == "rootfs" {
			continue
		}
		if mp == "" || mountBlacklist[strings.TrimRight(mp, "/")] {
			continue
		}
		if strings.HasPrefix(mp, "/rom") || strings.HasPrefix(mp, "/tmp/") {
			continue
		}
		m := Mount{Device: dev, Mount: mp, FSType: fs}
		m.USB = strings.HasPrefix(dev, "/dev/sd") || strings.HasPrefix(dev, "/dev/mmcblk") || strings.HasPrefix(dev, "/dev/disk")
		if m.USB {
			m.Writable = dirWritable(mp)
			if used, free, err := diskUsage(mp); err == nil {
				_ = used
				m.FreeMB = free >> 20
			}
		}
		// 也列出已挂载的 ext4/fuseblk（部分 USB 盘用 /dev/sda1 之外的命名）
		if !m.USB && (fs == "ext4" || fs == "vfat" || fs == "exfat" || fs == "ntfs" || fs == "fuseblk" || fs == "f2fs") {
			m.Writable = dirWritable(mp)
			if _, free, err := diskUsage(mp); err == nil {
				m.FreeMB = free >> 20
			}
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].USB != out[j].USB {
			return out[i].USB
		}
		return strings.Count(out[i].Mount, "/") < strings.Count(out[j].Mount, "/")
	})
	return out
}

func usbRoot() string {
	for _, m := range listMounts() {
		if m.USB && m.Writable {
			return filepath.Join(m.Mount, "fnparse")
		}
	}
	return ""
}
