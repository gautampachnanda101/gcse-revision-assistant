//go:build !windows

package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// totalRAMGB returns installed memory in whole GB, or 0 if unknown.
func totalRAMGB() int {
	if hostOS == "darwin" {
		out, err := execCommand("sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return 0
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return int(n >> 30)
	}
	f, err := os.Open(procMeminfo)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return int((kb + 512*1024) >> 20)
		}
	}
	return 0
}
