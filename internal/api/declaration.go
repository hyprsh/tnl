package api

import "github.com/hyprsh/tnl/internal/declare"

// PathDeclaration is an Outpost's own Declaration: POST sends the whole
// Declaration (a DeclarationRequest, answered with a DeclarationReply); GET
// fetches the Outpost's Routes and waiting lines (a MyRoutes). Both are for
// `outpost:<name>` only. Recorded in docs/spec/v1.md, Tunnel API.
const PathDeclaration = "/v1/declaration"

// DeclarationRequest is the Outpost's whole Declaration, even an empty one.
type DeclarationRequest struct {
	Routes []declare.Line `json:"routes"`
}

// Outcomes of a line.
const (
	LineAdded     = "added"
	LineChanged   = "changed"
	LineUnchanged = "unchanged"
	LineWaiting   = "waiting"
)

// DeclaredLine is what became of one line.
type DeclaredLine struct {
	Hostname string `json:"hostname"`
	Outcome  string `json:"outcome"`
	// Reason says why a line waits.
	Reason string `json:"reason,omitempty"`
}

// DeclarationReply answers a Declaration: the outcome of each line, the
// Hostnames of lines that are gone, the Routes the CLI had made that it
// replaced, and the test dials' warnings.
type DeclarationReply struct {
	Lines    []DeclaredLine `json:"lines"`
	Removed  []string       `json:"removed,omitempty"`
	Switched []string       `json:"switched,omitempty"`
	Handovers
	Warnings
}

// MyRoutes answers GET PathDeclaration: the Outpost's Routes and waiting
// lines as the Edge has them, sorted by Hostname.
type MyRoutes struct {
	Routes []MyRoute `json:"routes"`
}

// MyRoute is one Route or waiting line.
type MyRoute struct {
	declare.Line
	Waiting bool   `json:"waiting,omitempty"`
	Reason  string `json:"reason,omitempty"` // why it waits, in the Edge's log wording
	// Holder is the Outpost that holds the Hostname, when that is why it waits.
	Holder string `json:"holder,omitempty"`
}
