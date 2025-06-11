package executor

import (
	"fmt"
	"os"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

// GetServerInfo returns server information
func (e *Executor) GetServerInfo() models.ServerInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return models.ServerInfo{
		RuntimeID:     "go-runtime",
		StartTime:     e.startTime,
		LastExecTime:  e.lastExecTime,
		WorkingDir:    e.workingDir,
		Plugins:       e.config.Server.Plugins,
		Username:      e.username,
		UserID:        e.userID,
		FileViewerURL: fmt.Sprintf("http://localhost:%d", e.config.Server.FileViewerPort),
		SystemStats:   e.GetSystemStats(),
	}
}

// GetSystemStats returns system statistics using gopsutil
func (e *Executor) GetSystemStats() models.SystemStats {
	pid := int32(os.Getpid())
	proc, err := process.NewProcess(pid)
	if err != nil {
		e.logger.Warnf("Failed to get process info: %v", err)
		return models.SystemStats{
			CPUPercent: 0.0,
			Memory: models.MemoryStats{
				RSS:     0,
				VMS:     0,
				Percent: 0.0,
			},
			Disk: models.DiskStats{
				Total:   0,
				Used:    0,
				Free:    0,
				Percent: 0.0,
			},
			IO: models.IOStats{
				ReadBytes:  0,
				WriteBytes: 0,
			},
		}
	}

	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		e.logger.Warnf("Failed to get CPU percent: %v", err)
		cpuPercent = 0.0
	}

	memInfo, err := proc.MemoryInfo()
	if err != nil {
		e.logger.Warnf("Failed to get memory info: %v", err)
		memInfo = &process.MemoryInfoStat{RSS: 0, VMS: 0}
	}

	memPercent, err := proc.MemoryPercent()
	if err != nil {
		e.logger.Warnf("Failed to get memory percent: %v", err)
		memPercent = 0.0
	}

	workingDir := e.workingDir
	if workingDir == "" {
		workingDir = "/"
	}
	diskUsage, err := disk.Usage(workingDir)
	if err != nil {
		e.logger.Warnf("Failed to get disk usage: %v", err)
		diskUsage = &disk.UsageStat{Total: 0, Used: 0, Free: 0, UsedPercent: 0.0}
	}

	ioCounters, err := proc.IOCounters()
	if err != nil {
		e.logger.Warnf("Failed to get IO counters: %v", err)
		ioCounters = &process.IOCountersStat{ReadBytes: 0, WriteBytes: 0}
	}

	return models.SystemStats{
		CPUPercent: cpuPercent,
		Memory: models.MemoryStats{
			RSS:     memInfo.RSS,
			VMS:     memInfo.VMS,
			Percent: memPercent,
		},
		Disk: models.DiskStats{
			Total:   diskUsage.Total,
			Used:    diskUsage.Used,
			Free:    diskUsage.Free,
			Percent: diskUsage.UsedPercent,
		},
		IO: models.IOStats{
			ReadBytes:  ioCounters.ReadBytes,
			WriteBytes: ioCounters.WriteBytes,
		},
	}
}
