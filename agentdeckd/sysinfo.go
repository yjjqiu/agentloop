package main

import (
	"math"
	"os"
	"runtime"
	"time"
)

// sysMemBar 是整机内存用量（字节）。
type sysMemBar struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Available uint64  `json:"available"`
	Percent   float64 `json:"percent"`
}

// sysDiskBar 是数据目录所在磁盘的用量（字节）。
type sysDiskBar struct {
	Path    string  `json:"path"`
	Total   uint64  `json:"total"`
	Used    uint64  `json:"used"`
	Free    uint64  `json:"free"`
	Percent float64 `json:"percent"`
}

// sysSnapshot 是顶栏资源小插件的数据。
type sysSnapshot struct {
	OS       string         `json:"os"`
	At       string         `json:"at"`
	Mem      sysMemBar      `json:"mem"`
	Disk     sysDiskBar     `json:"disk"`
	Self     uint64         `json:"self"`     // AgentDeck 服务自身内存
	Total    uint64         `json:"total"`    // 服务 + 全部终端会话
	Sessions []sessionUsage `json:"sessions"` // 每个终端的占用明细
}

// collectSysSnapshot 读整机内存与数据目录所在磁盘。
// 容器里 /proc/meminfo 与 statfs 反映的是宿主机资源和宿主磁盘，正好是用户想看的。
func collectSysSnapshot(dataDir string) sysSnapshot {
	total, available := readMemInfo()
	if available > total {
		available = total
	}
	used := total - available

	dpath := dataDir
	if dpath == "" {
		dpath = "."
	}
	dtotal, dfree := readDiskInfo(dpath)
	if dfree > dtotal {
		dfree = dtotal
	}

	return sysSnapshot{
		OS:   runtime.GOOS,
		At:   time.Now().Format(time.RFC3339),
		Self: processMem(os.Getpid()),
		Mem: sysMemBar{
			Total:     total,
			Used:      used,
			Available: available,
			Percent:   ratioPercent(used, total),
		},
		Disk: sysDiskBar{
			Path:    dpath,
			Total:   dtotal,
			Used:    dtotal - dfree,
			Free:    dfree,
			Percent: ratioPercent(dtotal-dfree, dtotal),
		},
	}
}

// sysSnapshot 在整机数据上补上 AgentDeck 自己的终端明细。
// Self 只算服务进程本身，Total 才是服务 + 所有会话。
func (a *App) sysSnapshot() sysSnapshot {
	snap := collectSysSnapshot(a.dataDir)
	snap.Sessions = a.sessions.Usage()
	snap.Total = snap.Self
	for _, usage := range snap.Sessions {
		snap.Total += usage.Mem
	}
	return snap
}

// percentOf 计算百分比并保留一位小数；总量为 0 时返回 0。
func ratioPercent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(used)*1000/float64(total)) / 10
}
