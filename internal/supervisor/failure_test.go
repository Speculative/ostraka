package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchErrorRoundTripAndClear(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "supervisor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeDispatchError(root, "item-1", errors.New("quota exceeded")); err != nil {
		t.Fatal(err)
	}
	if got := ReadDispatchError(root, "item-1"); !strings.Contains(got, "quota exceeded") {
		t.Errorf("dispatch error = %q", got)
	}
	if err := clearDispatchError(root, "item-1"); err != nil {
		t.Fatal(err)
	}
	if got := ReadDispatchError(root, "item-1"); got != "" {
		t.Errorf("cleared dispatch error = %q", got)
	}
}
