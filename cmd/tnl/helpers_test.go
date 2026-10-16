package main

import "bytes"

// tnl runs a command the way main does and returns what it printed, its exit code
// and the error it would print.
func tnl(args ...string) (out string, code int, err error) {
	var buf bytes.Buffer
	old := stdout
	stdout = &buf
	defer func() { stdout = old }()
	err = commands[args[0]](args[1:])
	return buf.String(), exitCode(err), err
}
