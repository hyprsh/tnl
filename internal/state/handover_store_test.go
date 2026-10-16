package state

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

func openWaiting(t *testing.T) *Store {
	t.Helper()
	lg, _ := newLog()
	store, err := Open(t.TempDir(), lg)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(api.ActorLocal, func(st *State) error {
		w := waitingState(t)
		st.Outposts, st.Routes, st.Zones, st.Declarations, st.NextAddr = w.Outposts, w.Routes, w.Zones, w.Declarations, w.NextAddr
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestAChangeThatFreesAHostnameReportsTheLineThatGotIt(t *testing.T) {
	store := openWaiting(t)
	handed, err := store.UpdateReport(api.ActorLocal, func(st *State) error {
		st.RemoveRoutesVia("nas")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(handed) != 1 || handed[0].Outpost != "newbox" {
		t.Fatalf("handed %+v", handed)
	}
	if r, _ := store.Snapshot().Route("photos.example.com"); r.Outpost != "newbox" {
		t.Fatalf("Route %+v", r)
	}
}

func TestNoRouteAddSlipsInBetweenTheFreeAndTheHandover(t *testing.T) {
	for round := 0; round < 20; round++ {
		store := openWaiting(t)
		var rivalWon, sawFree atomic.Int32
		store.OnChange(func(st *State) {
			if _, held := st.Route("photos.example.com"); !held {
				sawFree.Add(1)
			}
		})
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				// What `route add` does: take the Hostname if it is free.
				store.Update(api.ActorLocal, func(st *State) error {
					if _, held := st.Route("photos.example.com"); held {
						return errors.New("held")
					}
					st.Routes = append(st.Routes, Route{Hostname: "photos.example.com", Outpost: "nas", Service: "http://localhost:2"})
					rivalWon.Add(1)
					return nil
				})
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			store.Update(api.ActorLocal, func(st *State) error {
				st.RemoveRoutesVia("nas")
				return nil
			})
		}()
		close(start)
		wg.Wait()
		if rivalWon.Load() != 0 || sawFree.Load() != 0 {
			t.Fatalf("round %d: a route add took the Hostname %d times, and %d snapshots had it free", round, rivalWon.Load(), sawFree.Load())
		}
		if r, _ := store.Snapshot().Route("photos.example.com"); r.Outpost != "newbox" {
			t.Fatalf("round %d: holder %+v", round, r)
		}
	}
}
