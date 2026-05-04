//go:build darwin

package tui

import (
	"os/exec"
	"strconv"
	"strings"
)

// macOS does not have /proc, and its ps/du flags differ from GNU coreutils.
// We shell out to BSD ps, sysctl, vm_stat, and du to gather equivalent metrics.
//
// CPU "ticks" on Linux are reported in CLK_TCK units (typically 100 Hz) via
// /proc/<pid>/stat. On macOS, getconf CLK_TCK also returns 100, and
// `ps -o time=` reports cumulative CPU time as MM:SS.cc (centiseconds), which
// happens to align exactly with CLK_TCK=100. We parse the time string to
// centiseconds so it's interchangeable with the Linux tick representation.

func listAllProcesses() map[int]*procInfo {
	// BSD ps suppresses the column header per-field by appending "=" to each
	// keyword, which is also accepted by GNU ps. -A == -e (all processes).
	cmd := exec.Command("ps", "-A", "-o", "pid=,ppid=,rss=")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	procs := make(map[int]*procInfo)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		ppid, _ := strconv.Atoi(fields[1])
		rss, _ := strconv.ParseInt(fields[2], 10, 64)
		procs[pid] = &procInfo{pid: pid, ppid: ppid, rss: rss}
	}
	return procs
}

func readAllProcTicks() map[int]int64 {
	ticks := make(map[int]int64)
	cmd := exec.Command("ps", "-A", "-o", "pid=,time=")
	output, err := cmd.Output()
	if err != nil {
		return ticks
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		t := parsePsTimeToTicks(fields[1])
		if t > 0 {
			ticks[pid] = t
		}
	}
	return ticks
}

// parsePsTimeToTicks converts BSD ps's cumulative CPU time field to CLK_TCK
// units (centiseconds, since macOS CLK_TCK == 100). Accepted forms:
//
//	MM:SS.cc
//	HH:MM:SS.cc
//	DD-HH:MM:SS.cc
//
// All math is done in integers — using floats here causes off-by-one rounding
// (e.g. 9:10.43 multiplied through float64 lands on 55042 instead of 55043).
func parsePsTimeToTicks(s string) int64 {
	var days int64
	if dashIdx := strings.Index(s, "-"); dashIdx >= 0 {
		d, err := strconv.ParseInt(s[:dashIdx], 10, 64)
		if err != nil {
			return 0
		}
		days = d
		s = s[dashIdx+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) == 0 {
		return 0
	}

	// The last segment carries fractional seconds (SS.cc). Earlier segments
	// are integer minute/hour units, accumulated in base 60.
	last := parts[len(parts)-1]
	var secondsWhole, centis int64
	if dotIdx := strings.Index(last, "."); dotIdx >= 0 {
		sw, err := strconv.ParseInt(last[:dotIdx], 10, 64)
		if err != nil {
			return 0
		}
		secondsWhole = sw

		cs := last[dotIdx+1:]
		// Normalize to 2 digits so "4" -> 40 centis, "456" -> 45 centis.
		switch {
		case len(cs) >= 2:
			cs = cs[:2]
		case len(cs) == 1:
			cs = cs + "0"
		default:
			cs = "00"
		}
		c, err := strconv.ParseInt(cs, 10, 64)
		if err != nil {
			return 0
		}
		centis = c
	} else {
		sw, err := strconv.ParseInt(last, 10, 64)
		if err != nil {
			return 0
		}
		secondsWhole = sw
	}

	var bigUnits int64 // accumulator in base 60 (ends up as total minutes)
	for _, p := range parts[:len(parts)-1] {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return 0
		}
		bigUnits = bigUnits*60 + n
	}
	totalSeconds := bigUnits*60 + secondsWhole + days*86400
	// CLK_TCK on macOS is 100, so 1 second == 100 ticks.
	return totalSeconds*100 + centis
}

func getDiskUsage(path string) int64 {
	// BSD du does not support -b (apparent bytes). -sk reports allocated KiB,
	// which is the disk usage users care about anyway.
	cmd := exec.Command("du", "-sk", path)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 1 {
		return 0
	}
	kb, _ := strconv.ParseInt(fields[0], 10, 64)
	return kb * 1024
}

func getTotalMemoryKB() int64 {
	cmd := exec.Command("sysctl", "-n", "hw.memsize")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	bytes, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0
	}
	return bytes / 1024
}

func getSystemMemPercent() float64 {
	cmd := exec.Command("vm_stat")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	pageSize := int64(4096)
	var active, wired, compressed int64
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			if idx := strings.Index(line, "page size of "); idx >= 0 {
				rest := line[idx+len("page size of "):]
				rfields := strings.Fields(rest)
				if len(rfields) > 0 {
					if ps, err := strconv.ParseInt(rfields[0], 10, 64); err == nil && ps > 0 {
						pageSize = ps
					}
				}
			}
			continue
		}
		if v, ok := parseVmStatField(line, "Pages active:"); ok {
			active = v
		} else if v, ok := parseVmStatField(line, "Pages wired down:"); ok {
			wired = v
		} else if v, ok := parseVmStatField(line, "Pages occupied by compressor:"); ok {
			compressed = v
		}
	}

	totalCmd := exec.Command("sysctl", "-n", "hw.memsize")
	totalOut, err := totalCmd.Output()
	if err != nil {
		return 0
	}
	totalBytes, err := strconv.ParseInt(strings.TrimSpace(string(totalOut)), 10, 64)
	if err != nil || totalBytes <= 0 {
		return 0
	}
	usedBytes := (active + wired + compressed) * pageSize
	if usedBytes <= 0 {
		return 0
	}
	pct := float64(usedBytes) / float64(totalBytes) * 100
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func parseVmStatField(line, prefix string) (int64, bool) {
	if !strings.HasPrefix(line, prefix) {
		return 0, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	rest = strings.TrimSuffix(rest, ".")
	v, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
