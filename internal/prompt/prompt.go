// Package prompt contains the static agent orientation shared by the
// supervisor's bootstrap prompt and the preamble CLI command.
package prompt

import (
	"fmt"
	"os"
	"path/filepath"
)

// AgentOrientation is the static orientation and final-reply guidance given
// to an agent. The caller supplies the exact command for its environment.
func AgentOrientation(replyCommand string) string {
	return fmt.Sprintf(`Use your normal harness turns and tools while working. The initial prompt contains the complete item context. Work and validate normally. If this work establishes durable, verified project facts that will help future sessions, update the agent-curated project brief with a complete replacement using ostraka project brief replace --content-stdin. Preserve useful existing facts, keep it concise and factual, and do not add one-off task details. The brief is limited to 6000 characters.

When finished, send one final Ostraka item reply using the exact command below. Do not send that reply as a progress acknowledgement: it hands the item back to the user and ends this dispatch. Pass real multiline content through stdin; never put literal \n text in the reply.

Threading guidance: keep supporting explanation inline, but create a direct
subthread with "ostraka item add --parent <root-or-child-id> --channel inbox
--status pending-user --title <one-line-title> --body
<self-contained-question>" when an in-scope
branch is independently discussable or likely to need multiple exchanges.
Creating from a child attaches a sibling; never create a grandchild. A
subthread normally starts pending-user for a question to the user or
backlog when parked. For work outside this item's scope, suggest a related
top-level item with "ostraka item suggest --related <item-id> ..."; proposals
need the user's keep/start decision before they become active work.

Final reply command:
%s`, replyCommand)
}

// ReplyCommand returns the command an agent should use to append its final
// reply. projectRoot is the directory containing cmd/ostraka when the local
// repository command should be preferred; an empty or unrelated root uses the
// installed CLI form.
func ReplyCommand(projectRoot, itemID string) string {
	if _, err := os.Stat(filepath.Join(projectRoot, "cmd", "ostraka", "main.go")); err == nil {
		return fmt.Sprintf("go run ./cmd/ostraka item turn %s --actor agent --content-stdin", itemID)
	}
	return fmt.Sprintf("ostraka item turn %s --actor agent --content-stdin", itemID)
}
