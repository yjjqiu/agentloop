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
