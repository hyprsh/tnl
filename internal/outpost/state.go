package outpost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/hyprsh/tnl/internal/keys"
)

// StateVersion is the schema version of the Outpost's state.json.
const StateVersion = 1

// FileName is the state file inside the state directory.
const FileName = "state.json"

// State is what an Outpost remembers: enough to reconnect without a token. It
// holds no Routes. Its name arrives with the join exchange in slice 3.
type State struct {
	PrivateKey  keys.Key
	Address     netip.Addr // this Outpost's Tunnel address
	EdgeAddress string     // the Edge's --address, resolved afresh at each start
	EdgePort    int
	EdgeKey     keys.Key
	TokenSHA256 string // of the token that created the state
}

type diskState struct {
	Version     int    `json:"version"`
	PrivateKey  string `json:"private_key"`
	Address     string `json:"address"`
	EdgeAddress string `json:"edge_address"`
	EdgePort    int    `json:"edge_port"`
	EdgeKey     string `json:"edge_public_key"`
	TokenSHA256 string `json:"token_sha256"`
}

// loadState reads dir's state.json. It returns (nil, nil) when there is none.
func loadState(dir string) (*State, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", FileName, err)
	}
	if head.Version != StateVersion {
		return nil, fmt.Errorf("%s has schema version %d; this tnl knows version %d", FileName, head.Version, StateVersion)
	}
	var d diskState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	s := &State{EdgeAddress: d.EdgeAddress, EdgePort: d.EdgePort, TokenSHA256: d.TokenSHA256}
	if err := s.PrivateKey.UnmarshalText([]byte(d.PrivateKey)); err != nil {
		return nil, fmt.Errorf("%s: private_key: %v", FileName, err)
	}
	if err := s.EdgeKey.UnmarshalText([]byte(d.EdgeKey)); err != nil {
		return nil, fmt.Errorf("%s: edge_public_key: %v", FileName, err)
	}
	if s.Address, err = netip.ParseAddr(d.Address); err != nil {
		return nil, fmt.Errorf("%s: address: %v", FileName, err)
	}
	if s.EdgeAddress == "" || s.EdgePort < 1 || s.EdgePort > 65535 {
		return nil, fmt.Errorf("%s: no usable Edge address and port", FileName)
	}
	return s, nil
}

// saveState writes state.json whole: the directory is made 0700, the file is
// 0600, and the write is a temp file renamed into place.
func saveState(dir string, s *State) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("state directory %s must be mode 0700: %w", dir, err)
	}
	priv, _ := s.PrivateKey.MarshalText()
	edge, _ := s.EdgeKey.MarshalText()
	b, err := json.MarshalIndent(diskState{
		Version: StateVersion, PrivateKey: string(priv), Address: s.Address.String(),
		EdgeAddress: s.EdgeAddress, EdgePort: s.EdgePort, EdgeKey: string(edge), TokenSHA256: s.TokenSHA256,
	}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, FileName)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
