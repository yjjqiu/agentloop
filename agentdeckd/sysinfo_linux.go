//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// readMemInfo 解析 /proc/meminfo，返回物理内存总量与可用量（字节）。
// 容器里读到的是宿主机的数据，除非用了 lxcfs。
func readMemInfo() (total, available uint64) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var free, buffers, cached uint64
	haveAvailable := false
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		value *= 1024 // /proc/meminfo 单位是 kB
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value
		case "MemAvailable":
			available, haveAvailable = value, true
		case "MemFree":
			free = value
		case "Buffers":
			buffers = value
		case "Cached":
			cached = value
		}
	}
	// 老内核没有 MemAvailable，用 Free + Buffers + Cached 兜底。
	if !haveAvailable {
		available = free + buffers + cached
	}
	return total, available
}

// readDiskInfo 返回 path 所在文件系统的总量与可用量（字节）。
func readDiskInfo(path string) (total, free uint64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	blockSize := uint64(stat.Bsize)
	return stat.Blocks * blockSize, stat.Bavail * blockSize
}
