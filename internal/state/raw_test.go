package state

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRawIsAWholeStateFileWhileChangesRun(t *testing.T) {
	lg, _ := newLog()
	dir := t.TempDir()
	s, err := Open(dir, lg)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			name := fmtName(i)
			_ = s.Update("local", func(st *State) error {
				st.Zones = append(st.Zones, Zone{Name: name, Mode: PerHostname})
				return nil
			})
		}
	}()
	for i := 0; i < 200; i++ {
		if _, err := decode(s.Raw()); err != nil {
			t.Fatalf("Raw is not a whole state file: %v", err)
		}
	}
	close(stop)
	<-done
	disk, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s.Raw(), disk) {
		t.Fatal("Raw differs from state.json")
	}
}

func fmtName(i int) string {
	return "z" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)) + string(rune('a'+(i/17576)%26)) + ".example.com"
}
