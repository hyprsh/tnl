package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/settings"
)

func TestConnectRefusesAnUnknownVariable(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("TNL_TOKEN", "")
	t.Setenv("TNL_ALOW", "localhost:3000")
	_, err := parseConnect(t)
	if exitCode(err) != 78 || !strings.Contains(err.Error(), "TNL_ALOW") || !strings.Contains(err.Error(), "did you mean TNL_ALLOW") {
		t.Fatalf("err=%v code=%d", err, exitCode(err))
	}
}

func TestConnectRouteLinesFromEitherFormParseToTheSameDeclaration(t *testing.T) {
	clearConnectEnv(t)
	line := "media.example.com http://localhost:8096 --host-header jelly"
	t.Setenv("TNL_ROUTE_MEDIA", line)
	fromEnv, err := parseConnect(t)
	if err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("TNL_ROUTE_MEDIA") // t.Setenv restores it
	fromFlag, err := parseConnect(t, "--route", line)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Declaration{{Hostname: "media.example.com", Service: "http://localhost:8096", HostHeader: "jelly"}}
	if !reflect.DeepEqual(fromEnv.declaration, want) || !reflect.DeepEqual(fromFlag.declaration, want) {
		t.Fatalf("%+v %+v", fromEnv.declaration, fromFlag.declaration)
	}
}

func TestConnectRouteErrorsExit78AndNameTheVariable(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("TNL_ROUTE_MEDIA", "media.example.com http://x --host-heder v")
	_, err := parseConnect(t)
	if exitCode(err) != 78 || !strings.Contains(err.Error(), "TNL_ROUTE_MEDIA: unknown option --host-heder") {
		t.Fatalf("err=%v code=%d", err, exitCode(err))
	}
	t.Setenv("TNL_ROUTE_MEDIA", "media.example.com http://x")
	_, err = parseConnect(t, "--route", "other.example.com http://y")
	if exitCode(err) != 78 || !strings.Contains(err.Error(), "TNL_ROUTE_MEDIA") {
		t.Fatalf("mixed: err=%v code=%d", err, exitCode(err))
	}
}
