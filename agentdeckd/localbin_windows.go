//go:build windows

package main

// defaultLocalBin 是 LoopForge Local 入口脚本的默认位置。
const defaultLocalBin = `E:\LoopForge\local\bin\loopforge.mjs`

// normalizeLocalBin 在 Windows 上原样保留配置值。
func normalizeLocalBin(path string) string { return path }
