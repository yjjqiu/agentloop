//go:build !windows

package main

// secrets_other.go：非 Windows 没有统一的系统密钥库，返回错误让上层退回明文保存
// （config.json 权限是 0600）。要做系统级加密时在这里接 keyring 即可。

import "errors"

var errNoKeyring = errors.New("当前平台没有系统密钥库")

func protectSecret(plain []byte) ([]byte, error) { return nil, errNoKeyring }

func unprotectSecret(sealed []byte) ([]byte, error) { return nil, errNoKeyring }
