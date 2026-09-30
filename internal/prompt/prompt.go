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
this dispatch. For work likely to exceed one dispatch, prefer bounded
checkpoints: finish a coherent slice, leave a concise substantive handoff
describing what remains, and make the next dispatch easy to resume from the
item and working tree. Do not invent self-scheduling commands or fake user
turns; use a self-scheduling or loop facility only when this prompt explicitly
provides one. Use actual multiline content, never literal \n text.`

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

const activitySectionText = `--- unhandled activity ---
{{range .Activities}}{{.Type}}: {{.Title}} ({{.SubjectID}}{{if .FromRootID}}, from={{.FromRootID}}{{end}}{{if .ToRootID}}, to={{.ToRootID}}{{end}}{{if .PreviousTitle}}, previous-title={{.PreviousTitle}}{{end}}, result={{.Result}}, actor={{.Actor}})
{{end}}Review each distinct affected subthread once with
"ostraka item show <child-id> --json" unless a later event requires another
read. Reconcile its decision with the root item and include the consequences in
your final root reply. For item metadata activity, use the subject ID shown in
the activity line and account for the change in your reply when relevant.`

const agentOrientationText = `Ostraka is the threaded work interface around this coding-agent conversation.
Treat an item as a durable work card, roughly like a Kanban card, whose turns
are the conversation for that work. Direct subthreads hold independently
discussable branches.

Your normal harness progress and partial responses are streamed into Ostraka
and are visible to the user. Use them for concise progress updates. A retained
provider trace, also called a partial trace in the JSON field "partial_traces",
is provider progress output captured during an agent dispatch, such as
reasoning or tool activity; it is not a posted conversation turn. It may be
linked to a posted agent turn or stand alone when a run ends before a final
response. Retained traces from earlier runs are included in fresh-session item
context when available; the complete journal is queryable with "ostraka item
show <item-id> --include-partial --json". Reserve the final Ostraka item reply
for the substantive handoff that ends the dispatch.

A standalone retained trace is a conversation hole: provider progress was
emitted, but no final agent turn was posted for that dispatch. In JSON, a
non-zero "turn_timestamp" links a trace to its posted agent turn; a zero
"turn_timestamp" means the trace is standalone and has no posted response.

For long items, pipe the JSON through jq so only the needed portion enters
context. Turn indexes are zero-based. Common queries:
  ostraka item show <item-id> --json | jq '.turns[-20:]'
  ostraka item show <item-id> --json | jq '[.turns[] | select(.actor == "user")]'
  ostraka item show <item-id> --include-partial --json | jq --argjson turn 42 '. as $item | ($item.turns[$turn].timestamp) as $ts | $item | .partial_traces = [.partial_traces[] | select(.turn_timestamp == $ts)]'

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

For work likely to exceed one dispatch, prefer bounded checkpoints: finish a
coherent slice, leave a concise substantive handoff describing what remains,
and make the next dispatch easy to resume from the item and working tree. Do
not invent self-scheduling commands or fake user turns; use a self-scheduling
or loop facility only when the prompt explicitly provides one.

Threading guidance: keep supporting explanation inline, but create a direct
subthread with "ostraka item add --parent <root-or-child-id> --channel inbox
--status pending-user --title <one-line-title> --body
<self-contained-question>" when an in-scope
branch is independently discussable or likely to need multiple exchanges.
Every new item has its own provider session and starts without this
conversation. Write its title and body for that cold start: include the
context, constraints, prior decisions, and desired outcome needed to act, but
omit unrelated history.
Creating from a child attaches a sibling; never create a grandchild. A
subthread normally starts pending-user for a question to the user or
backlog when parked. For work outside this item's scope, suggest a related
item with "ostraka item suggest --related <item-id> ..."; proposals
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
	Type          string
	Title         string
	SubjectID     string
	PreviousTitle string
	FromRootID    string
	ToRootID      string
	Result        string
	Actor         models.Actor
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
	return base + dispatchLengthGuidance + activitySection(activities)
}

const dispatchLengthGuidance = `

For work likely to exceed one dispatch, prefer bounded checkpoints: finish a
coherent slice, leave a concise substantive handoff describing what remains,
and make the next dispatch easy to resume from the item and working tree. Do
not invent self-scheduling commands or fake user turns; use a self-scheduling
or loop facility only when the prompt explicitly provides one.`

func activitySection(activities []models.Activity) string {
	if len(activities) == 0 {
		return ""
	}
	data := activitySectionData{Activities: make([]activityData, 0, len(activities))}
	for _, activity := range activities {
		title := activity.ItemTitle
		if title == "" {
			title = activity.ChildTitle
		}
		if title == "" {
			title = activity.ItemID
		}
		if title == "" {
			title = activity.ChildID
		}
		subjectID := activity.ItemID
		if subjectID == "" {
			subjectID = activity.ChildID
		}
		data.Activities = append(data.Activities, activityData{
			Type:          activity.Type,
			Title:         title,
			SubjectID:     subjectID,
			PreviousTitle: activity.PreviousTitle,
			FromRootID:    activity.FromRootID,
			ToRootID:      activity.ToRootID,
			Result:        activity.Result,
			Actor:         activity.Actor,
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
