package supervisor

import (
	"os"
	"path/filepath"
	"strings"
)

func dispatchErrorPath(root, itemID string) string {
	return filepath.Join(supervisorDir(root), "error-"+itemID+".txt")
}

// ReadDispatchError returns the most recent provider failure for itemID. The
// record is deliberately separate from the item file: it is operational
// state, not a user or agent turn, and must not affect conversation semantics.
func ReadDispatchError(root, itemID string) string {
	b, err := os.ReadFile(dispatchErrorPath(root, itemID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeDispatchError(root, itemID string, err error) error {
	message := "provider reported an unsuccessful turn"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = strings.TrimSpace(err.Error())
	}
	return writeAtomic(dispatchErrorPath(root, itemID), []byte(message+"\n"))
}

func clearDispatchError(root, itemID string) error {
	err := os.Remove(dispatchErrorPath(root, itemID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
