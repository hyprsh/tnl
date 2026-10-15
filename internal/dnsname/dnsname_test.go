package dnsname

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"App.Example.COM.": "app.example.com",
		"  a.b ":           "a.b",
		"already.lower":    "already.lower",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostOnly(t *testing.T) {
	for in, want := range map[string]string{
		"App.example.com":      "app.example.com",
		"app.example.com:8443": "app.example.com",
		"[::1]:443":            "::1",
		"[::1]":                "::1",
		"app.example.com.":     "app.example.com",
	} {
		if got := HostOnly(in); got != want {
			t.Errorf("HostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckLabel(t *testing.T) {
	long := make([]byte, 64)
	for i := range long {
		long[i] = 'a'
	}
	for label, ok := range map[string]bool{
		"home": true, "a-b": true, "9": true, string(long[:63]): true,
		"": false, "-a": false, "a-": false, "A": false, "a_b": false, "a b": false, string(long): false,
	} {
		if err := CheckLabel(label); (err == nil) != ok {
			t.Errorf("CheckLabel(%q) = %v, want ok=%v", label, err, ok)
		}
	}
}

func TestCheckName(t *testing.T) {
	for name, ok := range map[string]bool{
		"example.com": true, "a": true, "x.y.example.com": true,
		"": false, "a..com": false, "-a.com": false, "exa mple.com": false,
	} {
		if err := CheckName(name); (err == nil) != ok {
			t.Errorf("CheckName(%q) = %v, want ok=%v", name, err, ok)
		}
	}
	long := ""
	for len(long) < 254 {
		long += "abcdefghi."
	}
	if CheckName(long+"com") == nil {
		t.Error("a name over 253 characters passed")
	}
}
