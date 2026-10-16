package settings

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/declare"
)

func TestCheckEnvSuggestsTheNearestKnownName(t *testing.T) {
	err := CheckEnv([]string{"PATH=/bin", "TNL_ADRESS=example.com"}, []string{"TNL_ADDRESS", "TNL_STATE_DIR"}, "TNL_ROUTE_")
	if err == nil || !strings.Contains(err.Error(), "TNL_ADRESS") || !strings.Contains(err.Error(), "did you mean TNL_ADDRESS") {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "example.com") {
		t.Fatalf("the message echoes a value: %v", err)
	}
}

func TestCheckEnvWithoutAGoodGuessSaysOnlyUnknown(t *testing.T) {
	err := CheckEnv([]string{"TNL_QQQQQQQQQQQQ=1"}, []string{"TNL_ADDRESS"}, "")
	if err == nil || !strings.Contains(err.Error(), "TNL_QQQQQQQQQQQQ") || strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("%v", err)
	}
}

func TestCheckEnvAcceptsKnownNamesAndPrefixes(t *testing.T) {
	env := []string{"TNL_ADDRESS=x", "TNL_ROUTE_MEDIA=a b", "HOME=/", "TNLX=1"}
	if err := CheckEnv(env, []string{"TNL_ADDRESS"}, "TNL_ROUTE_"); err != nil {
		t.Fatal(err)
	}
}

func TestParseRoutesFromEitherFormGiveTheSameDeclaration(t *testing.T) {
	line := "Media.Example.com http://localhost:8096 --host-header jelly --insecure-skip-verify"
	fromEnv, _, err := ParseRoutes([]string{"TNL_ROUTE_MEDIA=" + line}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fromFlag, _, err := ParseRoutes(nil, []string{line})
	if err != nil {
		t.Fatal(err)
	}
	want := []declare.Line{{Hostname: "media.example.com", Service: "http://localhost:8096", HostHeader: "jelly", InsecureSkipVerify: true}}
	if !reflect.DeepEqual(fromEnv, want) || !reflect.DeepEqual(fromFlag, want) {
		t.Fatalf("%+v %+v", fromEnv, fromFlag)
	}
}

func TestParseRoutesSortsByHostname(t *testing.T) {
	d, _, err := ParseRoutes([]string{"TNL_ROUTE_B=b.example.com http://x", "TNL_ROUTE_A=z.example.com http://y"}, nil)
	if err != nil || len(d) != 2 || d[0].Hostname != "b.example.com" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestParseRoutesErrorsNameTheVariable(t *testing.T) {
	cases := []struct {
		name  string
		env   []string
		flags []string
		want  string
	}{
		{"unknown option", []string{"TNL_ROUTE_MEDIA=m.example.com http://x --host-heder v"}, nil, "TNL_ROUTE_MEDIA: unknown option --host-heder"},
		{"missing url", []string{"TNL_ROUTE_A=m.example.com"}, nil, "TNL_ROUTE_A: "},
		{"empty", []string{"TNL_ROUTE_A="}, nil, "TNL_ROUTE_A: "},
		{"extra word", []string{"TNL_ROUTE_A=m.example.com http://x extra"}, nil, "TNL_ROUTE_A: "},
		{"option needs value", []string{"TNL_ROUTE_A=m.example.com http://x --host-header"}, nil, "TNL_ROUTE_A: --host-header needs a value"},
		{"option twice", []string{"TNL_ROUTE_A=m.example.com http://x --insecure-skip-verify --insecure-skip-verify"}, nil, "TNL_ROUTE_A: "},
		{"path", []string{"TNL_ROUTE_A=m.example.com http://x/y"}, nil, "TNL_ROUTE_A: "},
		{"scheme", []string{"TNL_ROUTE_A=m.example.com ftp://x"}, nil, "TNL_ROUTE_A: "},
		{"hostname", []string{"TNL_ROUTE_A=*.example.com http://x"}, nil, "TNL_ROUTE_A: "},
		{"label", []string{"TNL_ROUTE_A-B=m.example.com http://x"}, nil, "TNL_ROUTE_A-B: "},
		{"no label", []string{"TNL_ROUTE_=m.example.com http://x"}, nil, "TNL_ROUTE_: "},
		{"duplicate", []string{"TNL_ROUTE_A=m.example.com http://x", "TNL_ROUTE_B=M.example.com http://y"}, nil, "TNL_ROUTE_B: m.example.com is also on TNL_ROUTE_A"},
		{"flag error", nil, []string{"m.example.com http://x --nope"}, "--route m.example.com http://x --nope: unknown option --nope"},
		{"flag duplicate", nil, []string{"m.example.com http://x", "m.example.com http://y"}, "is also on"},
		{"mixed", []string{"TNL_ROUTE_A=m.example.com http://x"}, []string{"n.example.com http://y"}, "--route and TNL_ROUTE_A are both set"},
	}
	for _, c := range cases {
		_, _, err := ParseRoutes(c.env, c.flags)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
}
