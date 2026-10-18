package harness

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

// readLine reads one line from sc within the time limit.
func readLine(t *testing.T, sc *bufio.Scanner) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		if sc.Scan() {
			got <- sc.Text()
		} else {
			got <- "EOF"
		}
	}()
	select {
	case l := <-got:
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("no line within 5s")
		return ""
	}
}

func TestLogsServesTheEdgesOwnLinesAndFollowsNewOnes(t *testing.T) {
	t.Parallel()
	re := StartEdgeWith(t, EdgeOptions{})
	nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{})

	// An Outpost's Tunnel call to a local-only path is refused, and logged.
	if _, err := api.Call[api.OutpostList](Ctx(t), nas.API, http.MethodGet, api.PathOutposts, nil); err == nil {
		t.Fatal("an Outpost listed Outposts")
	}
	body, _, err := re.API.Download(Ctx(t), api.PathLogs+"?level=warn")
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(body)
	if l := readLine(t, sc); !strings.Contains(l, "request refused") {
		t.Errorf("first warn line = %q", l)
	}
	body.Close()

	// Follow: a line logged after the call arrives.
	fctx, cancel := context.WithCancel(Ctx(t))
	defer cancel()
	fb, _, err := re.API.Download(fctx, api.PathLogs+"?follow=1&n=1&level=warn")
	if err != nil {
		t.Fatal(err)
	}
	defer fb.Close()
	fsc := bufio.NewScanner(fb)
	readLine(t, fsc) // the last line held
	api.Call[api.OutpostList](Ctx(t), nas.API, http.MethodGet, api.PathOutposts, nil)
	if l := readLine(t, fsc); !strings.Contains(l, "request refused") {
		t.Errorf("followed line = %q", l)
	}
}

func TestAnOutpostCannotReadTheEdgesLogsThroughTheTunnel(t *testing.T) {
	t.Parallel()
	re := StartEdgeWith(t, EdgeOptions{})
	nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{})
	_, _, err := nas.API.Download(Ctx(t), api.PathLogs)
	if ae, ok := err.(*api.Error); !ok || ae.Status != http.StatusForbidden {
		t.Fatalf("err = %v, want 403", err)
	}
}

func TestLogsOnAnOutpostAreItsOwnAndNotTheEdges(t *testing.T) {
	t.Parallel()
	re := StartEdgeWith(t, EdgeOptions{})
	nas := re.ConnectWith(t, "nas", re.AddOutpost(t, "nas"), ConnectOptions{})

	body, _, err := nas.CLI.Download(Ctx(t), api.PathLogs)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	sc := bufio.NewScanner(body)
	n := 0
	for sc.Scan() {
		n++
		if strings.Contains(sc.Text(), "edge started") {
			t.Errorf("the Outpost served an Edge line: %q", sc.Text())
		}
	}
	if n == 0 {
		t.Error("the Outpost's ring is empty")
	}
}
