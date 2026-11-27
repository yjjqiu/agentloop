package main

import (
	"math"
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
	OS   string     `json:"os"`
	At   string     `json:"at"`
	Mem  sysMemBar  `json:"mem"`
	Disk sysDiskBar `json:"disk"`
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
		OS: runtime.GOOS,
		At: time.Now().Format(time.RFC3339),
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

// percentOf 计算百分比并保留一位小数；总量为 0 时返回 0。
func ratioPercent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(used)*1000/float64(total)) / 10
}
