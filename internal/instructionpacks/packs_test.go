package instructionpacks

import (
	"strings"
	"testing"
)

func TestAddPreservesInstructionsAndRejectsEditedPack(t *testing.T) {
	original := "# Local rules\n\nKeep this text.\n"
	installed, changed, err := Add(original, "numbered-plan")
	if err != nil || !changed {
		t.Fatalf("first add: changed=%v err=%v", changed, err)
	}
	if !strings.HasPrefix(installed, original) || !strings.Contains(installed, "# Numbered plan workflow") {
		t.Fatalf("existing instructions or pack missing: %q", installed)
	}
	again, changed, err := Add(installed, "numbered-plan")
	if err != nil || changed || again != installed {
		t.Fatalf("repeat add: changed=%v err=%v", changed, err)
	}
	edited := strings.Replace(installed, "# Numbered plan workflow", "# Custom plan workflow", 1)
	if _, _, err := Add(edited, "numbered-plan"); err == nil {
		t.Fatal("edited pack should require manual reconciliation")
	}
}

func TestAddDetectsPastedPack(t *testing.T) {
	content, err := Content("numbered-plan")
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := Add("Local rules\n\n"+content+"\n", "numbered-plan")
	if err != nil || changed || got != "Local rules\n\n"+content+"\n" {
		t.Fatalf("pasted pack duplicated: changed=%v err=%v", changed, err)
	}
	if _, _, err := Add("", "missing"); err == nil {
		t.Fatal("unknown pack accepted")
	}
}

func TestPackDiscoveryAndCoexistence(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no embedded packs discovered")
	}
	for _, name := range names {
		if content, err := Content(name); err != nil || content == "" {
			t.Fatalf("embedded pack %q: content=%q err=%v", name, content, err)
		}
	}

	other := "<!-- ostraka instruction pack: future-pack -->\nFuture rules.\n<!-- end ostraka instruction pack: future-pack -->\n"
	got, changed, err := Add(other, "numbered-plan")
	if err != nil || !changed {
		t.Fatalf("add alongside another pack: changed=%v err=%v", changed, err)
	}
	if !strings.HasPrefix(got, other) || !strings.Contains(got, "# Numbered plan workflow") {
		t.Fatalf("pack did not coexist with existing pack: %q", got)
	}
}
