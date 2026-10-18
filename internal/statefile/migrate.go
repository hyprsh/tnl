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
		return 0, &SchemaError{Name: name, Have: head.Version, Known: current}
	}
	for v := head.Version; v < current; v++ {
		if steps[v] == nil {
			return 0, &SchemaError{Name: name, Have: head.Version, Known: current}
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

// SchemaError is the refusal of a file whose schema this tnl doesn't read. Its
// message names both versions and the side to upgrade: a newer file wants a
// newer tnl; an older one that has no migration step wants the tnl that wrote
// it. A caller finds it with errors.As.
type SchemaError struct {
	Name        string
	Have, Known int
}

func (e *SchemaError) Error() string {
	if e.Have > e.Known {
		return fmt.Sprintf("%s has schema version %d; this tnl knows version %d: it was written by a newer tnl; upgrade this tnl", e.Name, e.Have, e.Known)
	}
	return fmt.Sprintf("%s has schema version %d; this tnl knows version %d: it was written by an older tnl and can't be migrated; start it with the tnl that wrote it", e.Name, e.Have, e.Known)
}

// Older reports whether the file's schema is older than this tnl knows.
func (e *SchemaError) Older() bool { return e.Have < e.Known }
