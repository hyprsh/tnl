package harness

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/api"
	"github.com/hyprsh/tnl/internal/render"
)

// `tnl logs -f` on an Outpost: its CLI talks to the relay socket, which serves
// the Outpost's own ring and must deliver a followed line as it is logged. The
// follow runs through render.Logs, the code the command runs.
func TestFollowingAnOutpostsLogsDeliversNewLinesAsTheyAreLogged(t *testing.T) {
	t.Parallel()
	re := StartEdgeWith(t, EdgeOptions{})
	re.AddZone(t, "test")
	nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{})

	fctx, cancel := context.WithCancel(Ctx(t))
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := render.Logs(fctx, pw, nas.CLI, api.LogsQuery{Follow: true, N: 1})
		pw.Close()
		done <- err
	}()
	defer func() {
		cancel()
		pr.Close()
		<-done
	}()
	sc := bufio.NewScanner(pr)
	readLine(t, sc) // the last line held

	// A route change through the Outpost's CLI is a line the Outpost logs.
	req := api.RouteAddRequest{Hostname: "app.test", Service: "http://127.0.0.1:1", Outpost: "nas"}
	if _, err := api.Call[api.RouteAddReply](Ctx(t), nas.CLI, http.MethodPost, api.PathRoutes, req); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		l := readLine(t, sc)
		if strings.Contains(l, "route change through this Outpost's CLI") {
			return
		}
	}
	t.Fatal("the line logged after the follow began never arrived")
}
