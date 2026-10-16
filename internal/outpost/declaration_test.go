package outpost

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/declare"
	"github.com/hyprsh/tnl/internal/wire"
)

func newTestDeclarer(t *testing.T, h http.HandlerFunc) *declarer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(api.VersionHeader, "v1")
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := &api.Client{Version: "v1", Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}}
	return newDeclarer(c, slog.New(slog.NewTextHandler(io.Discard, nil)), []DeclaredRoute{{
		Line: declare.Line{Hostname: "a.example.com", Service: "http://localhost:1"}, Label: "TNL_ROUTE_A",
	}})
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestADeclarationTheEdgeRefusesWholeIsSentAgainOnlyWhenTheEdgesVersionChanges(t *testing.T) {
	var posts atomic.Int32
	d := newTestDeclarer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "the Edge (v1) doesn't support the field routes; upgrade the Edge"})
			return
		}
		json.NewEncoder(w).Encode(api.MyRoutes{Routes: []api.MyRoute{}})
	})
	answer := func(version string) {
		d.answered(context.Background(), wire.Pong{Version: version, Declaration: &wire.DeclarationAnswer{Matches: false}})
	}
	idle := func() bool { d.mu.Lock(); defer d.mu.Unlock(); return !d.busy }

	answer("v1")
	waitUntil(t, "the first send", func() bool { return posts.Load() == 1 && idle() })
	for range 5 {
		answer("v1")
	}
	time.Sleep(50 * time.Millisecond)
	if n := posts.Load(); n != 1 {
		t.Fatalf("a refused Declaration was sent %d times for one Edge version", n)
	}
	answer("v2")
	waitUntil(t, "the send after the Edge's version changed", func() bool { return posts.Load() == 2 && idle() })
}

func TestADeclarationThatGetsAServerErrorIsSentAgainAtTheNextPing(t *testing.T) {
	var posts atomic.Int32
	d := newTestDeclarer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
			return
		}
		json.NewEncoder(w).Encode(api.MyRoutes{Routes: []api.MyRoute{}})
	})
	idle := func() bool { d.mu.Lock(); defer d.mu.Unlock(); return !d.busy }
	for i := int32(1); i <= 3; i++ {
		d.answered(context.Background(), wire.Pong{Version: "v1", Declaration: &wire.DeclarationAnswer{Matches: false}})
		waitUntil(t, "a send", func() bool { return posts.Load() == i && idle() })
	}
}

func TestAnErrorWithNoHTTPStatusIsNotARefusalOfTheDeclaration(t *testing.T) {
	var dials atomic.Int32
	c := &api.Client{Version: "v1", Dial: func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, &api.Error{Message: "no answer"}
	}}
	d := newDeclarer(c, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	idle := func() bool { d.mu.Lock(); defer d.mu.Unlock(); return !d.busy }
	for i := int32(1); i <= 2; i++ {
		d.answered(context.Background(), wire.Pong{Version: "v1", Declaration: &wire.DeclarationAnswer{Matches: false}})
		waitUntil(t, "a send", func() bool { return dials.Load() >= i && idle() })
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.didRefuse {
		t.Fatal("an error with no HTTP status stopped the Declaration being sent again")
	}
}

func TestAMatchingAnswerSendsNothing(t *testing.T) {
	var posts atomic.Int32
	d := newTestDeclarer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		json.NewEncoder(w).Encode(api.MyRoutes{Routes: []api.MyRoute{}})
	})
	d.answered(context.Background(), wire.Pong{Version: "v1", Declaration: &wire.DeclarationAnswer{Matches: true}})
	d.answered(context.Background(), wire.Pong{Version: "v1"}) // an Edge that sent no flag
	time.Sleep(50 * time.Millisecond)
	if posts.Load() != 0 {
		t.Fatal("sent although the digests matched")
	}
}
