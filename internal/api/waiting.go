package api

// WaitKind is why a declared line is not a Route. It is on the wire (in
// MyRoute), so an Outpost words the reason without reading the Edge's log text.
// The empty kind is a line that is a Route.
type WaitKind string

const (
	NotWaiting      WaitKind = ""
	WaitHeld        WaitKind = "held"         // another Outpost holds the Hostname
	WaitOutsideZone WaitKind = "outside_zone" // the Hostname is in no Zone
	WaitNotApplied  WaitKind = "not_applied"  // free and inside a Zone: it waits for the change that makes it a Route
)

// waitingWords are the reasons in the words of `ls` and `status`; holder is
// who holds the Hostname, for WaitHeld.
var waitingWords = map[WaitKind]func(holder string) string{
	WaitHeld:        func(holder string) string { return "taken by " + holder },
	WaitOutsideZone: func(string) string { return "outside every Zone" },
	WaitNotApplied:  func(string) string { return "not applied yet" },
}

// Words is the reason a line of this kind waits, in the words of `ls` and
// `status`; "" for a kind this build does not know.
func (k WaitKind) Words(holder string) string {
	if f, ok := waitingWords[k]; ok {
		return f(holder)
	}
	return ""
}
