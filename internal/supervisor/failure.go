package supervisor

import (
	"fmt"
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

// isAuthenticationFailure recognizes provider messages that require the user
// to restore credentials before another dispatch can succeed. Provider error
// text is not a stable API, so match common auth wording from both CLIs.
func isAuthenticationFailure(err error, structuredAuthFailure bool) bool {
	message := ""
	if err != nil {
		message = strings.ToLower(err.Error())
	}
	// Codex can use a 401 for model entitlement failures as well. Those are
	// authorization errors, but do not mean the user's credentials expired.
	for _, phrase := range []string{
		"not authorized to access this model",
		"not authorized to access the model",
		"not authorized for this model",
	} {
		if strings.Contains(message, phrase) {
			return false
		}
	}
	if structuredAuthFailure {
		return true
	}
	if err == nil {
		return false
	}
	for _, phrase := range []string{
		"failed to authenticate",
		"authentication failed",
		"authentication required",
		"authentication error",
		"not authenticated",
		"not logged in",
		"login required",
		"sign in required",
		"sign-in required",
		"oauth session expired",
		"oauth token expired",
		"access token expired",
		"token expired",
		"token has expired",
		"could not be refreshed",
		"could not refresh",
		"failed to refresh token",
		"failed to refresh oauth",
		"token refresh failed",
		"invalid api key",
		"incorrect api key",
		"missing bearer or basic authentication",
		"no credentials were found",
		"no codex credentials",
		"unauthorized",
	} {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}

func authenticationFailureError(err error) error {
	return fmt.Errorf("provider authentication failed; authenticate the selected provider and add a user turn to retry: %w", err)
}
