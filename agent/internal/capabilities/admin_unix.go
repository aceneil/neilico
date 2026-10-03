//go:build !windows

package capabilities

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func hasAdminPrivileges() bool {
	if os.Geteuid() == 0 {
		return true
	}
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
		if err == nil {
			const netAdmin = uint64(1) << 12
			return value&netAdmin != 0
		}
	}
	return false
}
