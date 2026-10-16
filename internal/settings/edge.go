package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/state"
)

// Prefixes of the declared lines of `tnl edge`.
const (
	ZonePrefix    = "TNL_ZONE_"
	OutpostPrefix = "TNL_OUTPOST_"
)

// RefuseRoutes refuses TNL_ROUTE_* in the Edge's environment: Routes are
// declared on the Outpost. It names the variable, never its value.
func RefuseRoutes(environ []string) error {
	var names []string
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, RoutePrefix) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return fmt.Errorf("%s: Routes are declared on the Outpost; set TNL_ROUTE_* there", names[0])
}

// ReadSecret returns a secret given either as a file (filePath, shown as
// fileName) or as the value of a variable (value, shown as varName), or "" when
// neither is set. Both set is refused. source says where the secret came from,
// for messages. No error carries the secret.
func ReadSecret(fileName, filePath, varName, value string) (secret, source string, err error) {
	switch {
	case filePath != "" && value != "":
		return "", "", fmt.Errorf("%s and %s are both set: give it one way", fileName, varName)
	case filePath != "":
		b, err := os.ReadFile(filePath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return "", "", fmt.Errorf("%s: %s is missing", fileName, filePath)
		case err != nil:
			return "", "", fmt.Errorf("%s: cannot read %s: %v", fileName, filePath, errors.Unwrap(err))
		}
		s := strings.TrimSpace(string(b))
		if s == "" {
			return "", "", fmt.Errorf("%s: %s is empty", fileName, filePath)
		}
		return s, fileName, nil
	case value != "":
		return strings.TrimSpace(value), varName, nil
	}
	return "", "", nil
}

// EdgeKey reads the Edge's WireGuard private key from --key-file
// (TNL_KEY_FILE) or TNL_KEY. It returns nil when neither is set, so the Edge
// generates its own. source names where the key came from.
func EdgeKey(fileName, filePath, value string) (key *keys.Key, source string, err error) {
	text, source, err := ReadSecret(fileName, filePath, "TNL_KEY", value)
	if err != nil || text == "" {
		return nil, "", err
	}
	var k keys.Key
	if err := k.UnmarshalText([]byte(text)); err != nil {
		return nil, "", fmt.Errorf("%s does not hold a WireGuard private key (a base64 key of 32 bytes, as `wg genkey` prints)", source)
	}
	return &k, source, nil
}

type declaredLine struct{ name, text string }

// collect gathers the lines of one kind: the variables with the prefix, sorted
// by name, then the repeated flag's lines. Setting both forms is refused.
func collect(environ, flagLines []string, prefix, flagName string) ([]declaredLine, error) {
	var lines []declaredLine
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if len(flagLines) > 0 {
			return nil, fmt.Errorf("%s and %s are both set: declare them one way", flagName, name)
		}
		if err := checkLabel(prefix, strings.TrimPrefix(name, prefix)); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		lines = append(lines, declaredLine{name, value})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })
	for i, f := range flagLines {
		// A flag line is named by its first word, unless that is an option; the
		// text itself is never echoed, since it may hold a value.
		name := fmt.Sprintf("%s #%d", flagName, i+1)
		if first := strings.Fields(f); len(first) > 0 && len(first[0]) <= 63 && dnsname.CheckName(dnsname.Normalize(first[0])) == nil {
			name = flagName + " " + first[0]
		}
		lines = append(lines, declaredLine{name, f})
	}
	return lines, nil
}

// ParseZones parses the TNL_ZONE_* variables in environ and the arguments of
// the repeated --zone flag. A wildcard line names one of providers (nil accepts
// any), and one of --token-file and --token (the latter in variables only).
// Every error names the variable, or the --zone line, and none echoes a value.
func ParseZones(environ, flagLines []string, providers []string) ([]state.DeclaredZone, error) {
	lines, err := collect(environ, flagLines, ZonePrefix, "--zone")
	if err != nil {
		return nil, err
	}
	var out []state.DeclaredZone
	seen := map[string]string{}
	for _, l := range lines {
		z, err := parseZone(l, strings.HasPrefix(l.name, ZonePrefix), providers)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", l.name, err)
		}
		if other, dup := seen[z.Name]; dup {
			return nil, fmt.Errorf("%s: Zone %s is also on %s", l.name, z.Name, other)
		}
		seen[z.Name] = l.name
		out = append(out, z)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parseZone(l declaredLine, fromVar bool, providers []string) (state.DeclaredZone, error) {
	words := strings.Fields(l.text)
	if len(words) == 0 {
		return state.DeclaredZone{}, errors.New("want <zone> [--wildcard PROVIDER (--token-file PATH | --token VALUE)]")
	}
	var z state.DeclaredZone
	name := dnsname.Normalize(words[0])
	switch {
	case strings.HasPrefix(words[0], "-"):
		return z, errors.New("want the Zone's name first")
	case strings.Contains(name, "*"):
		return z, errors.New("write the Zone's name without a wildcard; a wildcard certificate is a Zone option")
	}
	if err := dnsname.CheckName(name); err != nil {
		return z, fmt.Errorf("invalid Zone name: %v", err)
	}
	z.Name = name
	var provider, tokenFile, token string
	var gotProvider, gotFile, gotToken bool
	for rest := words[1:]; len(rest) > 0; rest = rest[1:] {
		opt, _, _ := strings.Cut(rest[0], "=") // never echo what follows an '='
		switch rest[0] {
		case "--wildcard", "--token-file", "--token":
			if len(rest) < 2 {
				return z, fmt.Errorf("%s needs a value", rest[0])
			}
			switch rest[0] {
			case "--wildcard":
				if gotProvider {
					return z, errors.New("--wildcard is given twice")
				}
				gotProvider, provider = true, rest[1]
			case "--token-file":
				if gotFile {
					return z, errors.New("--token-file is given twice")
				}
				gotFile, tokenFile = true, rest[1]
			case "--token":
				if !fromVar {
					return z, errors.New("--token is accepted only in a TNL_ZONE_* variable; a --zone line takes --token-file")
				}
				if gotToken {
					return z, errors.New("--token is given twice")
				}
				gotToken, token = true, rest[1]
			}
			rest = rest[1:]
		default:
			if strings.HasPrefix(opt, "-") {
				return z, fmt.Errorf("unknown option %s", opt)
			}
			return z, errors.New("unexpected word after the options")
		}
	}
	switch {
	case !gotProvider && (gotFile || gotToken):
		return z, errors.New("a token needs its provider: add --wildcard PROVIDER")
	case !gotProvider:
		return z, nil
	case gotFile == gotToken:
		return z, errors.New("a wildcard Zone needs exactly one of --token-file and --token")
	case providers != nil && !slices.Contains(providers, provider):
		return z, fmt.Errorf("unknown DNS provider; this build has %s", strings.Join(providers, ", "))
	}
	z.Provider = provider
	if gotToken {
		z.Token = token
		return z, nil
	}
	b, err := os.ReadFile(tokenFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return z, fmt.Errorf("token file %s is missing", tokenFile)
	case err != nil:
		return z, fmt.Errorf("token file %s cannot be read", tokenFile)
	}
	z.Token = strings.TrimSpace(string(b))
	if z.Token == "" {
		return z, fmt.Errorf("token file %s is empty", tokenFile)
	}
	return z, nil
}

// ParseOutposts parses the TNL_OUTPOST_* variables in environ and the
// arguments of the repeated --outpost flag. It refuses a name or key on two
// lines, and two names whose derived Tunnel addresses collide, naming both.
func ParseOutposts(environ, flagLines []string) ([]state.DeclaredOutpost, error) {
	lines, err := collect(environ, flagLines, OutpostPrefix, "--outpost")
	if err != nil {
		return nil, err
	}
	var out []state.DeclaredOutpost
	byName := map[string]string{}
	byKey := map[keys.Key]string{}
	byAddr := map[string]string{} // address -> name
	for _, l := range lines {
		o, err := parseOutpost(l.text)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", l.name, err)
		}
		if other, dup := byName[o.Name]; dup {
			return nil, fmt.Errorf("%s: Outpost %s is also on %s", l.name, o.Name, other)
		}
		if other, dup := byKey[o.Key]; dup {
			return nil, fmt.Errorf("%s: the same key is on %s", l.name, other)
		}
		if other, clash := byAddr[addr.Derive(o.Name).String()]; clash {
			return nil, fmt.Errorf("%s: Outposts %s and %s derive the same Tunnel address %s; rename one", l.name, other, o.Name, addr.Derive(o.Name))
		}
		byName[o.Name], byKey[o.Key], byAddr[addr.Derive(o.Name).String()] = l.name, l.name, o.Name
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parseOutpost(text string) (state.DeclaredOutpost, error) {
	words := strings.Fields(text)
	if len(words) != 2 {
		return state.DeclaredOutpost{}, errors.New("want <name> <public-key>")
	}
	var o state.DeclaredOutpost
	if err := o.Key.UnmarshalText([]byte(words[1])); err != nil {
		return o, errors.New("the public key is not a base64 WireGuard key of 32 bytes, as `wg pubkey` prints")
	}
	o.Name = words[0]
	if err := dnsname.CheckLabel(o.Name); err != nil {
		return o, fmt.Errorf("invalid Outpost name: %v", err)
	}
	return o, nil
}

// Redact removes each secret from msg. It is the last line of defence for text
// that leaves the process.
func Redact(msg string, secrets ...string) string {
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	return msg
}
