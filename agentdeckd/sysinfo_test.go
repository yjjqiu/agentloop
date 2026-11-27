package main

import (
	"os"
	"runtime"
	"testing"
)

func TestPercentOf(t *testing.T) {
	cases := []struct {
		used, total uint64
		want        float64
	}{
		{0, 0, 0},
		{0, 100, 0},
		{50, 100, 50},
		{1, 3, 33.3},
		{999, 1000, 99.9},
	}
	for _, c := range cases {
		if got := ratioPercent(c.used, c.total); got != c.want {
			t.Fatalf("ratioPercent(%d,%d)=%v，期望 %v", c.used, c.total, got, c.want)
		}
	}
}

// TestCollectSysSnapshot 在本机能取到数据时校验用量关系；取不到就跳过（容器/特殊平台）。
func TestCollectSysSnapshot(t *testing.T) {
	snap := collectSysSnapshot(t.TempDir())
	if snap.OS == "" || snap.At == "" {
		t.Fatalf("快照缺少基础字段: %#v", snap)
	}
	if snap.Mem.Total == 0 {
		t.Skip("当前平台拿不到内存信息")
	}
	if snap.Mem.Used > snap.Mem.Total || snap.Mem.Available > snap.Mem.Total {
		t.Fatalf("内存用量越界: %#v", snap.Mem)
	}
	if snap.Mem.Used+snap.Mem.Available != snap.Mem.Total {
		t.Fatalf("内存用量对不上: %#v", snap.Mem)
	}
	if snap.Disk.Total > 0 {
		if snap.Disk.Used > snap.Disk.Total || snap.Disk.Free > snap.Disk.Total {
			t.Fatalf("磁盘用量越界: %#v", snap.Disk)
		}
		if snap.Disk.Percent < 0 || snap.Disk.Percent > 100 {
			t.Fatalf("磁盘百分比越界: %#v", snap.Disk)
		}
	}
}

// TestProcessMemory 校验进程内存采集：支持的平台要能读到当前进程，进程树不小于自身。
func TestProcessMemory(t *testing.T) {
	self := processMem(os.Getpid())
	tree := processTreeMem(os.Getpid())
	switch runtime.GOOS {
	case "windows", "linux":
		if self == 0 || tree < self {
			t.Fatalf("进程内存异常: self=%d tree=%d", self, tree)
		}
	default:
		if self != 0 || tree != 0 {
			t.Fatalf("不支持采集的平台应返回 0: self=%d tree=%d", self, tree)
		}
	}
	if processTreeMem(0) != 0 || processMem(-1) != 0 {
		t.Fatal("非法 pid 应返回 0")
	}
}

// TestSessionUsageEmpty 会话为空时返回空列表，不报错。
func TestSessionUsageEmpty(t *testing.T) {
	if got := NewSessionManager().Usage(); len(got) != 0 {
		t.Fatalf("空管理器不该有会话: %#v", got)
	}
}
