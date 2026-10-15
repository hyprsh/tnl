//go:build unix

package logx

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// OnJournal reports whether f is the systemd journal stream. env is
// $JOURNAL_STREAM, "<dev>:<ino>" in decimal (systemd.exec(5)).
func OnJournal(f *os.File, env string) bool {
	dev, ino, ok := strings.Cut(env, ":")
	if !ok {
		return false
	}
	d, err1 := strconv.ParseUint(dev, 10, 64)
	i, err2 := strconv.ParseUint(ino, 10, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	var st syscall.Stat_t
	if syscall.Fstat(int(f.Fd()), &st) != nil {
		return false
	}
	return uint64(st.Dev) == d && uint64(st.Ino) == i // Dev is int32 on darwin
}
