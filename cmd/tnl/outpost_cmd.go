//go:build !outpost

package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hyprsh/tnl/internal/api"
)

var outpostGroup = newGroup("outpost")

func init() {
	outpostGroup.subs["add"] = outpostAdd
}

// outpostAdd prints the join token alone on stdout, so it can be captured. The
// pinned install line comes in slice 3.
func outpostAdd(args []string) error {
	c := newClientCommand("outpost add")
	pos, err := c.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: tnl outpost add <name>")
	}
	r, err := api.Call[api.OutpostAddResponse](context.Background(), c.client(), http.MethodPost, api.PathOutposts, api.OutpostAddRequest{Name: pos[0]})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, r.Token)
	return nil
}
