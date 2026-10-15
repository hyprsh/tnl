//go:build !unix

package logx

import "os"

// OnJournal is false where there is no journal.
func OnJournal(*os.File, string) bool { return false }
