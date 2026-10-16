package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/state"
)

var providers = []string{"cloudflare"}

func keyText(t *testing.T) (string, keys.Key) {
	t.Helper()
	_, pub, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pub.MarshalText()
	return string(b), pub
}

func TestZoneLinesFromEitherFormGiveTheSameZones(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(file, []byte("  filetoken\n"), 0o600)
	fromEnv, err := ParseZones([]string{"TNL_ZONE_B=Wild.Org. --wildcard cloudflare --token-file " + file, "TNL_ZONE_A=example.com"}, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	fromFlag, err := ParseZones(nil, []string{"wild.org --wildcard cloudflare --token-file " + file, "example.com"}, providers)
	if err != nil {
		t.Fatal(err)
	}
	want := []state.DeclaredZone{{Name: "example.com"}, {Name: "wild.org", Provider: "cloudflare", Token: "filetoken"}}
	if !reflect.DeepEqual(fromEnv, want) || !reflect.DeepEqual(fromFlag, want) {
		t.Fatalf("%+v %+v", fromEnv, fromFlag)
	}
}

func TestATokenInAVariableLineIsTheTokenAndNeverInAFlagLine(t *testing.T) {
	z, err := ParseZones([]string{"TNL_ZONE_MAIN=example.com --wildcard cloudflare --token s3cret"}, nil, providers)
	if err != nil || z[0].Token != "s3cret" {
		t.Fatalf("%+v %v", z, err)
	}
	_, err = ParseZones(nil, []string{"example.com --wildcard cloudflare --token s3cret"}, providers)
	if err == nil || strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "--token-file") {
		t.Fatalf("%v", err)
	}
}

func TestZoneErrorsNameTheVariableAndEchoNoValue(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	os.WriteFile(empty, nil, 0o600)
	cases := []struct {
		name  string
		env   []string
		flags []string
		want  string
	}{
		{"unknown option", []string{"TNL_ZONE_MAIN=example.com --wildcrd cloudflare"}, nil, "TNL_ZONE_MAIN: unknown option --wildcrd"},
		{"option with value", []string{"TNL_ZONE_MAIN=example.com --token=hunter2"}, nil, "TNL_ZONE_MAIN: unknown option --token"},
		{"bad zone", []string{"TNL_ZONE_MAIN=bad_name.com"}, nil, "TNL_ZONE_MAIN: invalid Zone name"},
		{"wildcard in the name", []string{"TNL_ZONE_MAIN=*.example.com"}, nil, "TNL_ZONE_MAIN: write the Zone's name without a wildcard"},
		{"two zones", []string{"TNL_ZONE_A=example.com", "TNL_ZONE_B=EXAMPLE.com"}, nil, "TNL_ZONE_B: Zone example.com is also on TNL_ZONE_A"},
		{"no token", []string{"TNL_ZONE_MAIN=example.com --wildcard cloudflare"}, nil, "TNL_ZONE_MAIN: a wildcard Zone needs exactly one of --token-file and --token"},
		{"both tokens", []string{"TNL_ZONE_MAIN=example.com --wildcard cloudflare --token a --token-file b"}, nil, "TNL_ZONE_MAIN: a wildcard Zone needs exactly one"},
		{"unknown provider", []string{"TNL_ZONE_MAIN=example.com --wildcard nope --token a"}, nil, "TNL_ZONE_MAIN: unknown DNS provider; this build has cloudflare"},
		{"missing file", []string{"TNL_ZONE_MAIN=example.com --wildcard cloudflare --token-file /no/such/file"}, nil, "TNL_ZONE_MAIN: token file /no/such/file is missing"},
		{"empty file", []string{"TNL_ZONE_MAIN=example.com --wildcard cloudflare --token-file " + empty}, nil, "is empty"},
		{"bad label", []string{"TNL_ZONE_A-B=example.com"}, nil, "TNL_ZONE_A-B: the label"},
		{"no label", []string{"TNL_ZONE_=example.com"}, nil, "TNL_ZONE_: the variable needs a label after TNL_ZONE_"},
		{"empty", []string{"TNL_ZONE_MAIN="}, nil, "TNL_ZONE_MAIN: want <zone>"},
		{"both forms", []string{"TNL_ZONE_MAIN=example.com"}, []string{"other.org"}, "--zone and TNL_ZONE_MAIN are both set"},
		{"flag line named", nil, []string{"example.com --wildcrd x"}, "--zone example.com: unknown option --wildcrd"},
	}
	for _, c := range cases {
		_, err := ParseZones(c.env, c.flags, providers)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
			continue
		}
		for _, secret := range []string{"hunter2"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: echoes a value: %v", c.name, err)
			}
		}
	}
}

func TestOutpostLinesFromEitherFormGiveTheSameOutposts(t *testing.T) {
	k, key := keyText(t)
	want := []state.DeclaredOutpost{{Name: "nas", Key: key}}
	fromEnv, err := ParseOutposts([]string{"TNL_OUTPOST_NAS=nas " + k}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fromFlag, err := ParseOutposts(nil, []string{"nas " + k})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromEnv, want) || !reflect.DeepEqual(fromFlag, want) {
		t.Fatalf("%+v %+v", fromEnv, fromFlag)
	}
}

func TestOutpostErrorsNameTheVariable(t *testing.T) {
	k1, _ := keyText(t)
	k2, _ := keyText(t)
	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"no key", []string{"TNL_OUTPOST_NAS=nas"}, "TNL_OUTPOST_NAS: want <name> <public-key>"},
		{"extra word", []string{"TNL_OUTPOST_NAS=nas " + k1 + " more"}, "TNL_OUTPOST_NAS: want <name> <public-key>"},
		{"bad key", []string{"TNL_OUTPOST_NAS=nas notakey"}, "TNL_OUTPOST_NAS: the public key is not a base64 WireGuard key"},
		{"bad name", []string{"TNL_OUTPOST_NAS=NAS_1 " + k1}, "TNL_OUTPOST_NAS: invalid Outpost name"},
		{"same name", []string{"TNL_OUTPOST_A=nas " + k1, "TNL_OUTPOST_B=nas " + k2}, "TNL_OUTPOST_B: Outpost nas is also on TNL_OUTPOST_A"},
		{"same key", []string{"TNL_OUTPOST_A=nas " + k1, "TNL_OUTPOST_B=pi " + k1}, "TNL_OUTPOST_B: the same key is on TNL_OUTPOST_A"},
	}
	for _, c := range cases {
		_, err := ParseOutposts(c.env, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if err != nil && (strings.Contains(err.Error(), k1) || strings.Contains(err.Error(), k2)) {
			t.Errorf("%s: echoes a key: %v", c.name, err)
		}
	}
}

func TestTwoNamesWhoseDerivedAddressesCollideAreRefusedNamingBoth(t *testing.T) {
	// Find a collision by search: the range holds 2^22 addresses, so about 2300
	// names find one by the birthday bound.
	seen := map[string]string{}
	var a, b string
	for i := 0; ; i++ {
		n := "n" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + itoa(i)
		d := addr.Derive(n).String()
		if o, ok := seen[d]; ok {
			a, b = o, n
			break
		}
		seen[d] = n
	}
	k1, _ := keyText(t)
	k2, _ := keyText(t)
	_, err := ParseOutposts([]string{"TNL_OUTPOST_A=" + a + " " + k1, "TNL_OUTPOST_B=" + b + " " + k2}, nil)
	if err == nil || !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), b) || !strings.Contains(err.Error(), "same Tunnel address") {
		t.Fatalf("%v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var s string
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}

func TestEdgeKeyFromAFileOrAVariable(t *testing.T) {
	k, key := keyText(t)
	file := filepath.Join(t.TempDir(), "key")
	os.WriteFile(file, []byte(k+"\n"), 0o600)
	got, src, err := EdgeKey("TNL_KEY_FILE", file, "")
	if err != nil || *got != key || src != "TNL_KEY_FILE" {
		t.Fatalf("%v %q %v", got, src, err)
	}
	got, src, err = EdgeKey("TNL_KEY_FILE", "", " "+k+" ")
	if err != nil || *got != key || src != "TNL_KEY" {
		t.Fatalf("%v %q %v", got, src, err)
	}
	if got, _, err := EdgeKey("TNL_KEY_FILE", "", ""); got != nil || err != nil {
		t.Fatalf("%v %v", got, err)
	}
}

func TestEdgeKeyRefusesBothFormsAndBadKeysWithoutEchoing(t *testing.T) {
	k, _ := keyText(t)
	file := filepath.Join(t.TempDir(), "key")
	os.WriteFile(file, []byte(k), 0o600)
	if _, _, err := EdgeKey("TNL_KEY_FILE", file, k); err == nil || strings.Contains(err.Error(), k) || !strings.Contains(err.Error(), "TNL_KEY") {
		t.Fatalf("both: %v", err)
	}
	if _, _, err := EdgeKey("TNL_KEY_FILE", "", "hunter2hunter2"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("bad: %v", err)
	}
	if _, _, err := EdgeKey("--key-file", "/no/such", ""); err == nil || !strings.Contains(err.Error(), "--key-file") {
		t.Fatalf("missing: %v", err)
	}
}

func TestRefuseRoutesNamesTheVariable(t *testing.T) {
	err := RefuseRoutes([]string{"HOME=/", "TNL_ROUTE_PHOTOS=photos.example.com http://x"})
	if err == nil || err.Error() != "TNL_ROUTE_PHOTOS: Routes are declared on the Outpost; set TNL_ROUTE_* there" {
		t.Fatalf("%v", err)
	}
	if err := RefuseRoutes([]string{"TNL_ADDRESS=x"}); err != nil {
		t.Fatal(err)
	}
}
