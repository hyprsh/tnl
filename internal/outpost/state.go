package outpost

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/hyprsh/tnl/internal/keys"
	"github.com/hyprsh/tnl/internal/statefile"
)

// StateVersion is the schema version of the Outpost's state.json.
// Version 2 added the Outpost's name.
const StateVersion = 2

// FileName is the state file inside the state directory.
const FileName = "state.json"

// State is what an Outpost remembers: enough to reconnect without a token. It
// holds no Routes.
type State struct {
	Name        string
	PrivateKey  keys.Key
	Address     netip.Addr // this Outpost's Tunnel address
	EdgeAddress string     // the Edge's --address, resolved afresh at each start
	EdgePort    int
	EdgeKey     keys.Key
	TokenSHA256 string // of the token that created the state
}

type diskState struct {
	Version     int    `json:"version"`
	Name        string `json:"name"`
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
	var d diskState
	if err := statefile.Decode(raw, FileName, StateVersion, &d); err != nil {
		return nil, err
	}
	s := &State{Name: d.Name, EdgeAddress: d.EdgeAddress, EdgePort: d.EdgePort, TokenSHA256: d.TokenSHA256}
	if err := s.PrivateKey.UnmarshalText([]byte(d.PrivateKey)); err != nil {
		return nil, fmt.Errorf("%s: private_key: %v", FileName, err)
	}
	if err := s.EdgeKey.UnmarshalText([]byte(d.EdgeKey)); err != nil {
		return nil, fmt.Errorf("%s: edge_public_key: %v", FileName, err)
	}
	if s.Address, err = netip.ParseAddr(d.Address); err != nil {
		return nil, fmt.Errorf("%s: address: %v", FileName, err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("%s: no name", FileName)
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
		Version: StateVersion, Name: s.Name, PrivateKey: string(priv), Address: s.Address.String(),
		EdgeAddress: s.EdgeAddress, EdgePort: s.EdgePort, EdgeKey: string(edge), TokenSHA256: s.TokenSHA256,
	}, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(filepath.Join(dir, FileName), append(b, '\n'))
}
