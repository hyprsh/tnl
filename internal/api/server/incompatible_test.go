package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
	"github.com/hyprsh/tnl/internal/edge/liveness"
)

func TestAnIncompatibleOutpostsCallsAreRefusedWithTheSpecsMessage(t *testing.T) {
	f := start(t, "1.2.3", api.ActorLocal)
	live := liveness.New(liveness.Config{EdgeVersion: "1.2.3"})
	live.Heard("laptop")
	live.SetVersion("laptop", "3.0.0")
	live.Heard("nas")
	live.SetVersion("nas", "1.9.0")
	srv := server.New(server.Config{Store: f.store, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "1.2.3", Role: "edge", Liveness: live})

	call := func(who, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.Handler(api.OutpostActor(who)).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	w := call("laptop", api.PathZones)
	want := "the Edge (1.2.3) doesn't accept this Outpost (3.0.0); upgrade the Edge"
	if w.Code != 403 || !strings.Contains(w.Body.String(), want) {
		t.Fatalf("incompatible: %d %s", w.Code, w.Body)
	}
	if w.Header().Get(api.VersionHeader) != "1.2.3" {
		t.Errorf("refusal carries no %s", api.VersionHeader)
	}
	if w := call("nas", api.PathZones); w.Code != 200 {
		t.Errorf("an Outpost inside the window: %d %s", w.Code, w.Body)
	}
}

// The Edge knows only that a field is unknown; the client, the newer side,
// knows which version added it and words the refusal.
func TestTheClientNamesTheVersionThatAddedAnUnknownField(t *testing.T) {
	api.FieldAdded["new_opt"] = api.FieldNote{Name: "--new-opt", Since: "1.5.0"}
	t.Cleanup(func() { delete(api.FieldAdded, "new_opt") })
	f := start(t, "1.4.2", api.ActorLocal)
	_, err := api.Call[api.ZoneAddReply](context.Background(), f.client, http.MethodPost, api.PathZones, map[string]any{"name": "example.com", "new_opt": true})
	want := "the Edge (1.4.2) doesn't support `--new-opt`; it needs 1.5 or newer; upgrade the Edge"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestAnUnknownFieldIsRefusedWholeNamingBothVersionsAndTheSideToUpgrade(t *testing.T) {
	f := start(t, "1.4.2", api.ActorLocal)
	req := httptest.NewRequest("POST", api.PathZones, strings.NewReader(`{"name":"example.com","new_opt":true}`))
	w := httptest.NewRecorder()
	server.New(server.Config{Store: f.store, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "1.4.2", Role: "edge"}).
		Handler(api.ActorLocal).ServeHTTP(w, req)
	want := "the Edge (1.4.2) doesn't support `new_opt`; it needs a newer version; upgrade the Edge"
	if w.Code != 400 || !strings.Contains(w.Body.String(), want) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := w.Header().Get(api.UnknownFieldHeader); got != "new_opt" {
		t.Errorf("%s = %q", api.UnknownFieldHeader, got)
	}
	if len(f.store.Snapshot().Zones) != 0 {
		t.Error("a refused request changed the state")
	}
}
