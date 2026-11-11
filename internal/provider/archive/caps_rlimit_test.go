//go:build !race

package archive

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strconv"
	"syscall"
)

// limitAddressSpace sets RLIMIT_AS to the process's own VmSize plus 512 MiB:
// above its startup by construction, and far below the gigabytes an
// uncapped decoder asks for, so that decoder dies on the limit instead of
// taking the host's memory.
func limitAddressSpace() error {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(bytes.NewReader(status))
	for sc.Scan() {
		rest, ok := bytes.CutPrefix(sc.Bytes(), []byte("VmSize:"))
		if !ok {
			continue
		}
		kb, err := strconv.ParseUint(string(bytes.TrimSuffix(bytes.TrimSpace(rest), []byte(" kB"))), 10, 64)
		if err != nil {
			return err
		}
		limit := kb<<10 + 512<<20
		return syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: limit, Max: limit})
	}
	return errors.New("no VmSize in /proc/self/status")
}
