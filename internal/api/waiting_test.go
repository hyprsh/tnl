package api

import (
	"context"
	"net/http"
	"testing"
)

func TestWaitKindWordsAreThoseOfLsAndStatus(t *testing.T) {
	for kind, want := range map[WaitKind]string{
		WaitHeld: "taken by nas", WaitOutsideZone: "outside every Zone", WaitNotApplied: "not applied yet",
		NotWaiting: "", "from_the_future": "",
	} {
		if got := kind.Words("nas"); got != want {
			t.Errorf("%q: %q, want %q", kind, got, want)
		}
	}
}

// A newer Edge may add a field to the Outpost's own Routes; the Outpost still
// reads the rest, where Call refuses it.
func TestAnOutpostReadsItsRoutesWithAFieldItDoesNotKnow(t *testing.T) {
	body := `{"routes":[{"hostname":"a.example.com","service":"http://x:1","waiting":true,"wait_kind":"held","holder":"nas","new":1}]}`
	c := serve(t, PathDeclaration, body)
	ctx := context.Background()
	if _, err := Call[MyRoutes](ctx, c, http.MethodGet, PathDeclaration, nil); err == nil {
		t.Fatal("Call read a field it doesn't know")
	}
	got, _, err := GetLenient[MyRoutes](ctx, c, PathDeclaration)
	if err != nil || len(got.Routes) != 1 || got.Routes[0].WaitKind != WaitHeld || got.Routes[0].Holder != "nas" {
		t.Fatalf("%v %+v", err, got)
	}
}
