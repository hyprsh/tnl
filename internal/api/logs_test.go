package api

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// One check of a logs query serves the CLI and the server.
func TestLogsQueryValidateRefusesWhatTheServerRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    LogsQuery
		ok   bool
	}{
		{"empty", LogsQuery{}, true},
		{"all fields", LogsQuery{Follow: true, N: 5, Since: time.Minute, Level: "warn", JSON: true}, true},
		{"negative n", LogsQuery{N: -1}, false},
		{"negative since", LogsQuery{Since: -time.Second}, false},
		{"unknown level", LogsQuery{Level: "loud"}, false},
	} {
		err := tc.q.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: Validate = %v", tc.name, err)
		}
		var ae *Error
		if err != nil && (!errors.As(err, &ae) || ae.Status != http.StatusBadRequest) {
			t.Errorf("%s: %v is not a 400", tc.name, err)
		}
	}
	for _, q := range []string{"n=-1", "since=-1s", "level=loud", "n=x", "since=x"} {
		v, _ := url.ParseQuery(q)
		if _, err := ParseLogsQuery(v); err == nil {
			t.Errorf("ParseLogsQuery(%q) accepted it", q)
		}
	}
}
