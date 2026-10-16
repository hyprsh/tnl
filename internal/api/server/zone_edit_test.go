package server_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

func TestAnOutpostMayNotEditZones(t *testing.T) {
	o := start(t, "1.2.3", api.OutpostActor("nas"))
	_, err := api.Call[api.ZoneEditReply](context.Background(), o.client, http.MethodPost, api.PathZoneEdit, api.ZoneEditRequest{Name: "example.com", PerHostname: true})
	var e *api.Error
	if !errors.As(err, &e) || e.Status != http.StatusForbidden {
		t.Fatalf("err=%v", err)
	}
}
