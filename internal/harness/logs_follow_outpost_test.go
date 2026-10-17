package harness

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
)

// `tnl logs -f` on an Outpost: its CLI talks to the relay socket, which serves
// the Outpost's own ring and must deliver a followed line as it is logged.
func TestFollowingAnOutpostsLogsDeliversNewLinesAsTheyAreLogged(t *testing.T) {
	re := StartEdgeWith(t, EdgeOptions{})
	nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{})

	fctx, cancel := context.WithCancel(ctx(t))
	defer cancel()
	q := api.LogsQuery{Follow: true, N: 1}
	body, _, err := nas.CLI.Download(fctx, api.PathLogs+"?"+q.Values().Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	sc := bufio.NewScanner(body)
	readLine(t, sc) // the last line held

	// A route change through the Outpost's CLI is a line the Outpost logs.
	req := api.RouteAddRequest{Hostname: "app.test", Service: "http://127.0.0.1:1", Outpost: "nas"}
	api.Call[api.RouteAddReply](ctx(t), nas.CLI, http.MethodPost, api.PathRoutes, req)
	for i := 0; i < 20; i++ {
		l := readLine(t, sc)
		if strings.Contains(l, "route change through this Outpost's CLI") {
			return
		}
	}
	t.Fatal("the line logged after the follow began never arrived")
}
