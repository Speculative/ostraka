package instructionpacks

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed *.md
var files embed.FS

var names = func() []string {
	paths, err := fs.Glob(files, "*.md")
	if err != nil {
		panic(err)
	}
	for i, path := range paths {
		paths[i] = strings.TrimSuffix(path, ".md")
	}
	return paths
}()

func Names() []string { return append([]string(nil), names...) }

func Content(name string) (string, error) {
	for _, known := range names {
		if name == known {
			b, err := files.ReadFile(name + ".md")
			return strings.TrimSpace(string(b)), err
		}
	}
	return "", fmt.Errorf("unknown instruction pack %q", name)
}

// Add appends a marked pack while preserving existing instructions. An edited
// marked installation is left for the user to reconcile explicitly.
func Add(existing, name string) (string, bool, error) {
	content, err := Content(name)
	if err != nil {
		return "", false, err
	}
	start := "<!-- ostraka instruction pack: " + name + " -->"
	end := "<!-- end ostraka instruction pack: " + name + " -->"
	block := start + "\n" + content + "\n" + end
	if strings.Contains(existing, start) || strings.Contains(existing, end) {
		if strings.Count(existing, start) != 1 || strings.Count(existing, end) != 1 || !strings.Contains(existing, block) {
			return "", false, fmt.Errorf("instruction pack %q has edited or incomplete markers; reconcile it manually", name)
		}
		return existing, false, nil
	}
	if strings.Contains(existing, content) {
		return existing, false, nil
	}
	if strings.TrimSpace(existing) == "" {
		return block + "\n", true, nil
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + block + "\n", true, nil
}
