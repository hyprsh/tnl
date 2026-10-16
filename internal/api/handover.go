package api

import "fmt"

// Served is a waiting line that became a Route in the change that freed its
// Hostname or added its Zone.
type Served struct {
	Outpost  string `json:"outpost"`
	Hostname string `json:"hostname"`
}

// Handovers is how a reply to a change that can free a Hostname or add a Zone
// says which waiting lines it turned into Routes. It is `served` on the reply,
// left out when empty. The CLI prints a line for each.
type Handovers struct {
	Served []Served `json:"served,omitempty"`
}

// Lines are the sentences the CLI prints, such as "newbox declared
// photos.example.com and now serves it".
func (h Handovers) Lines() []string {
	var out []string
	for _, s := range h.Served {
		out = append(out, fmt.Sprintf("%s declared %s and now serves it", s.Outpost, s.Hostname))
	}
	return out
}
