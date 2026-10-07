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

func TestIsAuthenticationFailure(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		err                   error
		structuredAuthFailure bool
		want                  bool
	}{
		{
			name: "Claude OAuth refresh expired",
			err:  errors.New("Failed to authenticate: OAuth session expired and could not be refreshed"),
			want: true,
		},
		{
			name: "Codex missing credentials",
			err:  errors.New("codex app-server: unexpected status 401 Unauthorized: Missing bearer or basic authentication in header"),
			want: true,
		},
		{
			name:                  "structured Codex unauthorized",
			err:                   errors.New("request failed"),
			structuredAuthFailure: true,
			want:                  true,
		},
		{
			name: "invalid API key",
			err:  errors.New("401 Unauthorized: Incorrect API key provided"),
			want: true,
		},
		{
			name:                  "model entitlement despite structured unauthorized",
			err:                   errors.New("401 Unauthorized: You are not authorized to access this model"),
			structuredAuthFailure: true,
			want:                  false,
		},
		{
			name: "rate limit",
			err:  errors.New("429 quota exceeded"),
			want: false,
		},
		{name: "nil error", err: nil, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthenticationFailure(tc.err, tc.structuredAuthFailure); got != tc.want {
				t.Errorf("isAuthenticationFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
