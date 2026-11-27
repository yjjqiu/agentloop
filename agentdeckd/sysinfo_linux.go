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

// processMem 返回单个进程的常驻内存（字节），读 /proc/<pid>/statm 的第二列。
func processMem(pid int) uint64 {
	if pid <= 0 {
		return 0
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

// readPPID 从 /proc/<pid>/stat 里取父进程号。
// comm 字段可能含空格和括号，所以从最后一个 ')' 之后开始切。
func readPPID(pid int) int {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	text := string(raw)
	idx := strings.LastIndexByte(text, ')')
	if idx < 0 || idx+2 >= len(text) {
		return 0
	}
	fields := strings.Fields(text[idx+1:])
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

// processTreeMem 返回 pid 及其全部子孙进程的常驻内存合计。
func processTreeMem(pid int) uint64 {
	if pid <= 0 {
		return 0
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return processMem(pid)
	}
	children := make(map[int][]int, len(entries))
	rss := make(map[int]uint64, len(entries))
	for _, entry := range entries {
		proc, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		ppid := readPPID(proc)
		if ppid <= 0 {
			continue
		}
		children[ppid] = append(children[ppid], proc)
		rss[proc] = processMem(proc)
	}

	total := uint64(0)
	seen := make(map[int]bool, 16)
	queue := []int{pid}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		total += rss[cur]
		queue = append(queue, children[cur]...)
	}
	// 根进程可能是刚 fork 出来、stat 还没读到父进程号的情况，兜底补一次。
	if total == 0 {
		total = processMem(pid)
	}
	return total
}
