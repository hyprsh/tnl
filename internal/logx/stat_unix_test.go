//go:build unix

package logx

import (
	"os"
	"syscall"
	"testing"
)

func statOf(t *testing.T, f *os.File) (dev, ino uint64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	return uint64(st.Dev), uint64(st.Ino)
}
