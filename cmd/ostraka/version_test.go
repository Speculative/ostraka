package main

import (
	"bytes"
	"runtime/debug"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	for _, version := range []string{
		"v0.4.0",
		"v0.5.0-rc.1",
		"v0.4.1-0.20261014203754-123456789abc",
		"v0.4.0+dirty",
		"(devel)",
		"",
	} {
		t.Run(version, func(t *testing.T) {
			info := &debug.BuildInfo{Main: debug.Module{Version: version}}
			want := version
			if want == "" {
				want = "(devel)"
			}
			if got := buildVersion(info); got != want {
				t.Fatalf("buildVersion() = %q, want %q", got, want)
			}
		})
	}
	if got := buildVersion(nil); got != "(devel)" {
		t.Fatalf("buildVersion(nil) = %q, want (devel)", got)
	}
}

func TestVersionCommandOutsideProject(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	versionCmd.SetOut(&out)
	t.Cleanup(func() { versionCmd.SetOut(nil) })
	cmd, _, err := rootCmd.Find([]string{"version"})
	if err != nil || cmd != versionCmd {
		t.Fatalf("version command not registered: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	info, _ := debug.ReadBuildInfo()
	if want := "ostraka " + buildVersion(info) + "\n"; out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Fatal("version accepted an extra argument")
	}
}
