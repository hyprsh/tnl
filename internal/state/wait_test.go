package state

import "testing"

func TestWaitingSaysWhoHoldsTheHostname(t *testing.T) {
	st := waitingState(t)
	if w := st.Waiting("newbox", "photos.example.com"); w != (Wait{Kind: WaitHeld, Holder: "nas"}) || w.String() != "held by Outpost nas" {
		t.Errorf("held: %+v", w)
	}
	if w := st.Waiting("nas", "photos.example.com"); w.Kind != NotWaiting || w.String() != "" {
		t.Errorf("a Route: %+v", w)
	}
}
