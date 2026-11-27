//go:build !windows && !linux

package main

// 其它平台暂不采集资源数据，接口返回 0，界面显示「—」。
func readMemInfo() (total, available uint64) { return 0, 0 }

func readDiskInfo(path string) (total, free uint64) { return 0, 0 }
