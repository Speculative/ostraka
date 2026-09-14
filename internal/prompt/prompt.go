// Package prompt owns the agent-facing prompt text used by the supervisor and
// the preamble CLI command.
package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Speculative/ostraka/internal/models"
)

const bootstrapText = `Begin work on Ostraka item {{.ItemID}}.
This is Ostraka's initial prompt for this item. The item context available to
this dispatch is included below.

{{.Orientation}}

--- user-owned project instructions ---
{{.Instructions}}
--- end user-owned project instructions ---

--- agent-curated project brief ---
{{.Brief}}
--- end agent-curated project brief ---

--- Ostraka item context ---
{{.ItemContext}}
--- end Ostraka item context ---`

const continuationWithTurnsText = `This continues the conversation on Ostraka item {{.ItemID}}.
Do not repeat session-start orientation or re-read or re-announce startup skills
already applied unless new information makes them relevant.

Continue the item's work from the unseen user turns below. Treat the batched
turns as one ordered user message. Later turns supersede earlier turns where
they conflict; otherwise address them together.

--- unseen user turns ---
{{range .UserTurns}}--- user turn {{.Number}} ---
{{.Content}}
--- end user turn {{.Number}} ---
{{end}}--- end unseen user turns ---

Starting a new Ostraka turn does not by itself require revalidation. Run tests
or linters when warranted by code or configuration changes made during this
item, by a prior incomplete or failed check, or by the current request. Do not
rerun a successful check against unchanged inputs merely to reorient.

Do not use the final Ostraka item reply for a progress-only acknowledgement;
ordinary harness progress updates remain visible to the user. Send one
substantive "ostraka item turn" when the work is ready to hand back; it ends
this dispatch. Use actual multiline content, never literal \n text.`

const activityOnlyText = `This continues the conversation on Ostraka item {{.ItemID}}.
No unseen user turns are included in this dispatch. Review the subthread updates
below without repeating session-start orientation or reloading the root item.`

const continuationWithoutTurnText = `This continues the conversation on Ostraka item {{.ItemID}}.
No new user turns or subthread updates are included. Continue any outstanding
work already present in this conversation. Read the item with
"ostraka item show {{.ItemID}} --json" only if you cannot determine the
outstanding request or necessary context from the conversation.

Do not repeat session-start orientation or re-read or re-announce startup skills
already applied unless new information makes them relevant. Starting a new
Ostraka turn does not by itself require revalidation. Run checks only when the
outstanding work, a prior incomplete or failed check, or changed inputs warrant
them. Then send one substantive Ostraka item reply; it ends this dispatch.`

const activitySectionText = `--- unhandled subthread activity ---
{{range .Activities}}{{.Type}}: {{.Title}} ({{.ChildID}}, result={{.Result}}, actor={{.Actor}})
{{end}}Review each distinct affected subthread once with
"ostraka item show <child-id> --json" unless a later event requires another
read. Reconcile its decision with the root item and include the consequences in
your final root reply.`

const agentOrientationText = `Ostraka is the threaded work interface around this coding-agent conversation.
Treat an item as a durable work card, roughly like a Kanban card, whose turns
are the conversation for that work. Direct subthreads hold independently
discussable branches.

Your normal harness progress and partial responses are streamed into Ostraka
and are visible to the user. Use them for concise progress updates. Reserve the
final Ostraka item reply for the substantive handoff that ends the dispatch.

Use your normal harness turns and tools while working. The initial prompt
contains the item context available to this dispatch. Starting a new Ostraka
turn does not by itself require revalidation. Run tests or linters when
warranted by code or configuration changes made during this item, by a prior
incomplete or failed check, or by the current request. Do not rerun a successful
check against unchanged inputs merely to reorient. If this work establishes
information that is both long-lived and broadly relevant to most items, update
the agent-curated project brief with a complete replacement
using ostraka project brief replace --content-stdin. The supervisor includes the
brief when initiating every item, so it is for global project context such as
the project description, goals, and norms—not a log of recent changes. Do not
add medium-duration state (for example, current work or a recently fixed
decision), item-specific findings, temporary state, implementation plans, or
recommendations. If knowledge should outlive this item but only matters to a
subset of work, preserve its provenance by linking dependent items to the item
that established it where the item model supports that; do not duplicate it in
the brief. Preserve useful existing facts, keep the brief concise and factual,
and keep it within the 6000-character limit.

When finished, send one final Ostraka item reply using the exact command below.
Do not send that reply as a progress acknowledgement: it hands the item back to
the user and ends this dispatch. Pass real multiline content through stdin;
never put literal \n text in the reply.

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
{{.ReplyCommand}}`

var (
	bootstrapTemplate               = mustPromptTemplate("bootstrap", bootstrapText)
	continuationWithTurnsTemplate   = mustPromptTemplate("continuation-with-turns", continuationWithTurnsText)
	continuationWithoutTurnTemplate = mustPromptTemplate("continuation-without-turn", continuationWithoutTurnText)
	activityOnlyTemplate            = mustPromptTemplate("activity-only", activityOnlyText)
	activitySectionTemplate         = mustPromptTemplate("activity-section", activitySectionText)
	agentOrientationTemplate        = mustPromptTemplate("agent-orientation", agentOrientationText)
)

type bootstrapData struct {
	ItemID       string
	Orientation  string
	Instructions string
	Brief        string
	ItemContext  string
}

type continuationWithTurnsData struct {
	ItemID    string
	UserTurns []userTurnData
}

type continuationWithoutTurnData struct {
	ItemID string
}

type activityOnlyData struct {
	ItemID string
}

type userTurnData struct {
	Number  int
	Content string
}

type activitySectionData struct {
	Activities []activityData
}

type activityData struct {
	Type    string
	Title   string
	ChildID string
	Result  string
	Actor   models.Actor
}

type agentOrientationData struct {
	ReplyCommand string
}

// Bootstrap builds the complete prompt for the first turn in an item's agent
// session.
func Bootstrap(itemID, instructions, brief, itemContext, replyCommand string, activities []models.Activity) string {
	requireField("bootstrap", "ItemID", itemID)
	requireField("bootstrap", "ItemContext", itemContext)
	return renderPrompt(bootstrapTemplate, bootstrapData{
		ItemID:       itemID,
		Orientation:  AgentOrientation(replyCommand),
		Instructions: emptyContext(instructions),
		Brief:        emptyContext(brief),
		ItemContext:  itemContext,
	}) + activitySection(activities)
}

// Nudge builds the shorter prompt used for later turns in an existing item
// conversation.
func Nudge(itemID string, userTurns []string, activities []models.Activity) string {
	requireField("continuation", "ItemID", itemID)
	var base string
	if len(userTurns) > 0 {
		data := continuationWithTurnsData{
			ItemID:    itemID,
			UserTurns: make([]userTurnData, 0, len(userTurns)),
		}
		for i, content := range userTurns {
			data.UserTurns = append(data.UserTurns, userTurnData{Number: i + 1, Content: content})
		}
		base = renderPrompt(continuationWithTurnsTemplate, data)
	} else if len(activities) > 0 {
		base = renderPrompt(activityOnlyTemplate, activityOnlyData{ItemID: itemID})
	} else {
		base = renderPrompt(continuationWithoutTurnTemplate, continuationWithoutTurnData{
			ItemID: itemID,
		})
	}
	return base + activitySection(activities)
}

func activitySection(activities []models.Activity) string {
	if len(activities) == 0 {
		return ""
	}
	data := activitySectionData{Activities: make([]activityData, 0, len(activities))}
	for _, activity := range activities {
		title := activity.ChildTitle
		if title == "" {
			title = activity.ChildID
		}
		data.Activities = append(data.Activities, activityData{
			Type:    activity.Type,
			Title:   title,
			ChildID: activity.ChildID,
			Result:  activity.Result,
			Actor:   activity.Actor,
		})
	}
	return "\n\n" + renderPrompt(activitySectionTemplate, data)
}

func emptyContext(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// AgentOrientation is the static orientation and final-reply guidance given
// to an agent. The caller supplies the exact command for its environment.
func AgentOrientation(replyCommand string) string {
	requireField("agent-orientation", "ReplyCommand", replyCommand)
	return renderPrompt(agentOrientationTemplate, agentOrientationData{ReplyCommand: replyCommand})
}

func mustPromptTemplate(name, text string) *template.Template {
	return template.Must(template.New(name).Option("missingkey=error").Parse(text))
}

func renderPrompt(tmpl *template.Template, data any) string {
	var output strings.Builder
	if err := tmpl.Execute(&output, data); err != nil {
		panic(fmt.Sprintf("prompt: render %s: %v", tmpl.Name(), err))
	}
	return output.String()
}

func requireField(templateName, fieldName, value string) {
	if value == "" {
		panic(fmt.Sprintf("prompt: render %s: empty required field %s", templateName, fieldName))
	}
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
