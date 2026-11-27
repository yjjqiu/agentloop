//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx 对应 Win32 MEMORYSTATUSEX。
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	procGlobalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	procGetDiskFreeSpaceExW  = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
)

// readMemInfo 返回物理内存总量与可用量（字节）。
func readMemInfo() (total, available uint64) {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return 0, 0
	}
	return status.TotalPhys, status.AvailPhys
}

// readDiskInfo 返回 path 所在卷的总量与可用量（字节）。
func readDiskInfo(path string) (total, free uint64) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	var freeToCaller, totalBytes, totalFree uint64
	ok, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if ok == 0 {
		return 0, 0
	}
	return totalBytes, freeToCaller
}

// processMemoryCounters 对应 Win32 PROCESS_MEMORY_COUNTERS。
type processMemoryCounters struct {
	Size                       uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

const processQueryLimitedInformation = 0x1000

var (
	procGetProcessMemoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	kernel32Lib              = syscall.NewLazyDLL("kernel32.dll")
	procCreateSnapshot32     = kernel32Lib.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW      = kernel32Lib.NewProc("Process32FirstW")
	procProcess32NextW       = kernel32Lib.NewProc("Process32NextW")
	procOpenProcess          = kernel32Lib.NewProc("OpenProcess")
	procCloseHandle          = kernel32Lib.NewProc("CloseHandle")
)

// processEntry32 对应 Win32 PROCESSENTRY32W。
type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	CntThreads      uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

const (
	th32csSnapProcess = 0x00000002
	invalidHandle     = ^uintptr(0)
)

// processMem 返回单个进程的工作集（字节）。
func processMem(pid int) uint64 {
	if pid <= 0 {
		return 0
	}
	handle, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 || handle == invalidHandle {
		return 0
	}
	defer procCloseHandle.Call(handle)
	var counters processMemoryCounters
	counters.Size = uint32(unsafe.Sizeof(counters))
	ok, _, _ := procGetProcessMemoryInfo.Call(handle, uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if ok == 0 {
		return 0
	}
	return uint64(counters.WorkingSetSize)
}

// snapshotProcesses 列出当前所有进程的 (父 pid, 工作集)。
func snapshotProcesses() map[int]procInfo {
	out := make(map[int]procInfo)
	snap, _, _ := procCreateSnapshot32.Call(th32csSnapProcess, 0)
	if snap == 0 || snap == invalidHandle {
		return out
	}
	defer procCloseHandle.Call(snap)
	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	ok, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		pid := int(entry.ProcessID)
		out[pid] = procInfo{ppid: int(entry.ParentProcessID), mem: processMem(pid)}
		ok, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return out
}

// processTreeMem 返回 pid 及其全部子孙进程的工作集合计。
func processTreeMem(pid int) uint64 {
	if pid <= 0 {
		return 0
	}
	procs := snapshotProcesses()
	children := make(map[int][]int, len(procs))
	for child, info := range procs {
		children[info.ppid] = append(children[info.ppid], child)
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
		total += procs[cur].mem
		queue = append(queue, children[cur]...)
	}
	return total
}

// childProcessIDs 返回 parent 的直接子进程集合，用来在启动 ConPTY 后认出根进程。
func childProcessIDs(parent int) map[int]bool {
	out := make(map[int]bool)
	for pid, info := range snapshotProcesses() {
		if info.ppid == parent {
			out[pid] = true
		}
	}
	return out
}

// newChildProcessID 返回启动后新出现的子进程（ConPTY 的根进程）。
func newChildProcessID(parent int, before map[int]bool) int {
	for pid := range childProcessIDs(parent) {
		if !before[pid] {
			return pid
		}
	}
	return 0
}

// procInfo 是进程快照里的一项。
type procInfo struct {
	ppid int
	mem  uint64
}
