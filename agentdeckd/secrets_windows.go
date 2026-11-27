//go:build windows

package main

// secrets_windows.go：Windows 上用 DPAPI（CryptProtectData / CryptUnprotectData）
// 加密本机保存的访问令牌。加密结果只有同一 Windows 用户能解开。

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	crypt32DLL  = syscall.NewLazyDLL("crypt32.dll")
	kernel32DLL = syscall.NewLazyDLL("kernel32.dll")

	procCryptProtectData   = crypt32DLL.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32DLL.NewProc("CryptUnprotectData")
	procLocalFree          = kernel32DLL.NewProc("LocalFree")
)

// dataBlob 对应 Win32 的 DATA_BLOB。
type dataBlob struct {
	size uint32
	data *byte
}

// cryptProtectUIForbidden 保证 DPAPI 不会弹任何对话框。
const cryptProtectUIForbidden = 0x1

func protectSecret(plain []byte) ([]byte, error) {
	return dpapiCall(procCryptProtectData, plain)
}

func unprotectSecret(sealed []byte) ([]byte, error) {
	return dpapiCall(procCryptUnprotectData, sealed)
}

// dpapiCall 两个 API 参数表完全一致，只是方向相反。
func dpapiCall(proc *syscall.LazyProc, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	in := dataBlob{size: uint32(len(data)), data: &data[0]}
	var out dataBlob
	ret, _, err := proc.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // 描述
		0, // 附加熵
		0, // 保留
		0, // 提示结构
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("DPAPI 调用失败: %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return append([]byte(nil), unsafe.Slice(out.data, out.size)...), nil
}
