package caddyx

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var pageTime = time.Date(2026, 10, 16, 9, 30, 5, 0, time.UTC)

func render(status int, host, accept string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	writeErrorPage(rec, errorPage{Status: status, Host: host, ID: "0123456789abcdef", Accept: accept, Time: pageTime})
	return rec
}

func TestErrorPageMessagesPerStatus(t *testing.T) {
	for status, want := range map[int]string{
		404: "Nothing is served at app.example.com",
		502: "app.example.com isn't answering",
		503: "app.example.com is offline",
	} {
		rec := render(status, "app.example.com", "text/html,application/xhtml+xml")
		if rec.Code != status {
			t.Errorf("%d: status %d", status, rec.Code)
		}
		body := rec.Body.String()
		// The apostrophe is escaped in HTML.
		if !strings.Contains(strings.ReplaceAll(body, "&#39;", "'"), want) {
			t.Errorf("%d: page lacks %q:\n%s", status, want, body)
		}
		if !strings.Contains(body, "0123456789abcdef") || !strings.Contains(body, "2026-10-16 09:30:05 UTC") {
			t.Errorf("%d: page lacks the request ID or time", status)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("%d: Content-Type %q", status, got)
		}
	}
}

func TestErrorPageWithoutHTMLIsOneLineOfText(t *testing.T) {
	for _, accept := range []string{"", "*/*", "application/json"} {
		rec := render(502, "app.example.com", accept)
		if got := rec.Body.String(); got != "502 app.example.com isn't answering\n" {
			t.Errorf("Accept %q: body %q", accept, got)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("Accept %q: Content-Type %q", accept, got)
		}
	}
}

func TestErrorPageIsNoStoreSelfContainedAndAnonymous(t *testing.T) {
	for _, status := range []int{404, 502, 503} {
		for _, accept := range []string{"text/html", ""} {
			rec := render(status, "app.example.com", accept)
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%d: Cache-Control %q", status, rec.Header().Get("Cache-Control"))
			}
			all := strings.ToLower(rec.Body.String() + fmtHeaders(rec))
			for _, bad := range []string{"tnl", "outpost", "service", "version"} {
				if strings.Contains(all, bad) {
					t.Errorf("%d: response mentions %q", status, bad)
				}
			}
		}
	}
	body := render(404, "app.example.com", "text/html").Body.String()
	for _, bad := range []string{"<script", "http://", "https://", "src=", "@import", "url("} {
		if strings.Contains(body, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	if !strings.Contains(body, "<style>") || !strings.Contains(body, "prefers-color-scheme: dark") {
		t.Error("page needs inline CSS following prefers-color-scheme")
	}
}

func fmtHeaders(rec *httptest.ResponseRecorder) string {
	var b strings.Builder
	for k, vs := range rec.Header() {
		b.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
	}
	return b.String()
}

func TestErrorPageEscapesTheHost(t *testing.T) {
	body := render(404, `<script>alert(1)</script>`, "text/html").Body.String()
	if strings.Contains(body, "<script>") {
		t.Fatalf("host not escaped:\n%s", body)
	}
}
