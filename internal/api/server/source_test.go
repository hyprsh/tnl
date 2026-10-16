package server_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/api/server"
)

func TestHandlerBySourceSetsTheActorFromTheCallerAndRefusesAStranger(t *testing.T) {
	f := start(t, "1.0.0", api.ActorLocal)
	srv := server.New(server.Config{Store: f.store, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "1.0.0", Role: "edge"})
	h := srv.HandlerBySource(func(remote string) (api.Actor, error) {
		if strings.HasPrefix(remote, "100.64.0.2:") {
			return api.OutpostActor("laptop"), nil
		}
		return "", errors.New("no Outpost has this Tunnel address")
	})
	call := func(remote, method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("100.64.0.2:4000", "GET", api.PathZones); w.Code != 200 || w.Header().Get(api.VersionHeader) != "1.0.0" {
		t.Errorf("known Outpost reading Zones: %d", w.Code)
	}
	if w := call("100.64.0.2:4000", "POST", api.PathZones); w.Code != 403 {
		t.Errorf("known Outpost adding a Zone: %d", w.Code)
	}
	w := call("100.64.0.9:4000", "GET", api.PathZones)
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("stranger: %d %s", w.Code, w.Body)
	}
}
