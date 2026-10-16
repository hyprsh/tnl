package statefile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Steps are a file's migrations. Steps[n] changes a document of schema n into
// schema n+1, in place; Migrate sets the `version` field itself.
type Steps map[int]func(doc map[string]any) error

// Migrate brings the file at path to schema current. A file already at current
// is left alone (from is 0). An older one is migrated step by step, the file as
// it was is kept as path.v<n> (n is the schema it had) and the new one replaces
// it. A schema that is newer, or that has no step to take it forward, is
// refused as Decode refuses it, and nothing is written. name is the file's
// name for the errors.
func Migrate(path, name string, current int, steps Steps) (from int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return 0, fmt.Errorf("%s is not valid JSON: %w", name, err)
	}
	if head.Version == current {
		return 0, nil
	}
	if head.Version > current {
		return 0, unknownSchema(name, head.Version, current)
	}
	for v := head.Version; v < current; v++ {
		if steps[v] == nil {
			return 0, unknownSchema(name, head.Version, current)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	for v := head.Version; v < current; v++ {
		if err := steps[v](doc); err != nil {
			return 0, fmt.Errorf("%s: migrating from schema %d: %w", name, v, err)
		}
		doc["version"] = v + 1
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := Write(path+".v"+strconv.Itoa(head.Version), raw); err != nil {
		return 0, fmt.Errorf("keep the old %s: %w", name, err)
	}
	if err := Write(path, append(out, '\n')); err != nil {
		return 0, err
	}
	return head.Version, nil
}

func unknownSchema(name string, have, known int) error {
	return fmt.Errorf("%s has schema version %d; this tnl knows version %d", name, have, known)
}
