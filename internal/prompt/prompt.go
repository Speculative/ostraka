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
	return fmt.Sprintf(`Use your normal harness turns and tools while working. The initial prompt contains the complete item context. Work and validate normally. If this work establishes information that is both long-lived and broadly relevant to most items, update the agent-curated project brief with a complete replacement using ostraka project brief replace --content-stdin. The supervisor includes the brief when initiating every item, so it is for global project context such as the project description, goals, and norms—not a log of recent changes. Do not add medium-duration state (for example, current work or a recently fixed decision), item-specific findings, temporary state, implementation plans, or recommendations. If knowledge should outlive this item but only matters to a subset of work, preserve its provenance by linking dependent items to the item that established it where the item model supports that; do not duplicate it in the brief. Preserve useful existing facts, keep the brief concise and factual, and keep it within the 6000-character limit.

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
