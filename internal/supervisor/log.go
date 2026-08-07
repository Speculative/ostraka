package supervisor

import (
	"log"
	"os"
	"path/filepath"
)

func newLogger(root string) *log.Logger {
	f, err := os.OpenFile(filepath.Join(supervisorDir(root), "log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// Can't open the log file — fall back to stderr rather than eat errors silently.
		return log.New(os.Stderr, "[supervisor] ", log.LstdFlags)
	}
	return log.New(f, "", log.LstdFlags)
}
