package main

import (
	"strings"
	"testing"
)

func TestTurnContent(t *testing.T) {
	tests := []struct {
		name         string
		stdin        string
		args         []string
		contentStdin bool
		want         string
		wantErr      bool
	}{
		{name: "positional", args: []string{"id", "short reply"}, want: "short reply"},
		{name: "stdin preserves multiline content", stdin: "first line\nsecond line\n", args: []string{"id"}, contentStdin: true, want: "first line\nsecond line\n"},
		{name: "blank positional content", args: []string{"id", " \t\n"}, wantErr: true},
		{name: "blank stdin content", stdin: "\n\t", args: []string{"id"}, contentStdin: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := turnContent(strings.NewReader(tt.stdin), tt.args, tt.contentStdin)
			if (err != nil) != tt.wantErr {
				t.Fatalf("turnContent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("turnContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestItemTurnArgumentRules(t *testing.T) {
	original := turnFlags.contentStdin
	t.Cleanup(func() { turnFlags.contentStdin = original })

	turnFlags.contentStdin = false
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id", "content"}); err != nil {
		t.Fatalf("positional form rejected: %v", err)
	}
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id"}); err == nil {
		t.Fatal("missing positional content was accepted")
	}

	turnFlags.contentStdin = true
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id"}); err != nil {
		t.Fatalf("stdin form rejected: %v", err)
	}
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id", "content"}); err == nil {
		t.Fatal("stdin form accepted positional content")
	}
}
