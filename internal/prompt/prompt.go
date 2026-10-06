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
The item context is below.

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

const activitySectionText = `--- unhandled activity ---
{{range .Activities}}{{.Type}}: {{.Title}} ({{.SubjectID}}{{if .FromRootID}}, from={{.FromRootID}}{{end}}{{if .ToRootID}}, to={{.ToRootID}}{{end}}{{if .PreviousTitle}}, previous-title={{.PreviousTitle}}{{end}}, result={{.Result}}, actor={{.Actor}})
{{end}}`

const activityReviewGuidance = `Consider the activity lines as one batch. Read
every distinct affected child before consolidating: inspect its status and
last few turns with "ostraka item show <child-id> --json" (pipe long items
through jq). An archived child may have finished or been abandoned. If a
deleted child cannot be opened, use the activity line and available root
context to decide whether its work is still needed elsewhere. Ask the user to
clarify an outcome you cannot determine. Then assess the batch against the root
goal and remaining open children. For item metadata activity, use its subject
ID and mention it only if relevant.`

const mixedActivityGuidance = `Reconcile the child outcomes with the unseen
user turns in their given order. Address those turns fully, including requested
implementation or details about children. Point out a conflict between a child
outcome and a user request instead of silently choosing one.`

const activityFollowupGuidance = `Check open siblings' titles and bodies when a
child decision might change their work. Post a concise turn to an affected
sibling only when its work changes, citing the source child by ID. Create an
in-scope subthread only if the user asks for one or the root and proposed child
both have distinct discussions to continue. Carry sequential follow-on work
forward on the root; suggest related items for out-of-scope work. Treat a
child's outcome as new evidence for the root's existing goal. If it unblocks
work already authorized by the root goal and conversation, continue that work
here, including implementation. If the next step needs a user decision or new
authorization, ask here. Child closure alone does not authorize unrelated
work. In the final root reply, report the batch's combined effect on root
completion, next work, and items created or proposed. Recommend archiving only
when the root goal is met and every remaining child is archived. When reporting
activity, mention child content only where it justifies a conclusion, citing
its ID instead of recapping it. If the batch changes nothing, say so in one
sentence.`

const agentOrientationText = `Ostraka items are durable work cards. Turns are their conversations; direct
subthreads carry parallel work. Your normal harness progress is visible to the
user, so use it for concise updates. A retained provider trace is captured
progress from a dispatch, not a posted reply. Earlier traces are included in
fresh-session context when available. Read the complete conversation,
including traces, with:
  ostraka item show <item-id> --include-partial --json
A standalone partial_trace entry means the run ended without an agent reply.
For long items, select only the needed conversation entries with jq.

Use normal harness turns and tools while working. The item context below is
available now; starting a new Ostraka turn alone does not require
revalidation. Run checks when changed code, a failed or incomplete check, or
the request warrants them. Do not repeat successful checks against unchanged
inputs just to reorient.

Update the agent-curated project brief only for long-lived facts relevant to
most items, using:
  ostraka project brief replace --content-stdin
Replace it
completely, preserve useful existing facts, and keep it within 6000
characters. Keep current work, decisions, plans, and item-specific findings on
items. Link dependent items when narrower knowledge must persist.

When finished, send one substantive final reply with the exact command below.
It hands the item back and ends this dispatch. Pass real multiline content
through stdin, never literal \\n text. For work spanning dispatches, finish a
coherent slice and leave a handoff that makes the next dispatch easy to
resume. Do not invent self-scheduling commands or fake user turns.

Keep sequential work and blocking questions on this item. Create a direct
subthread only when it and this item will have distinct active discussions, or
when the user asks for a split. A child created from a child becomes its
sibling. New items have independent sessions and must be understandable from
their own title and body. For a parallel in-scope branch, use:
  ostraka item add --parent <root-or-child-id> --channel inbox --status pending-user --title <one-line-title> --body <self-contained-question>
Use backlog when parking a branch. The root reconciles closed children. For
out-of-scope work, suggest a related item with:
  ostraka item suggest --mentions <item-id> ...
The user decides whether to keep or start it.

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
	}) + activitySection(activities, false)
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
	return base + dispatchLengthGuidance + activitySection(activities, len(userTurns) > 0)
}

const dispatchLengthGuidance = `

For work likely to exceed one dispatch, prefer bounded checkpoints: finish a
coherent slice, leave a concise substantive handoff describing what remains,
and make the next dispatch easy to resume from the item and working tree. Do
not invent self-scheduling commands or fake user turns; use a self-scheduling
or loop facility only when the prompt explicitly provides one.`

func activitySection(activities []models.Activity, hasUserTurns bool) string {
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
	section := "\n\n" + renderPrompt(activitySectionTemplate, data) + activityReviewGuidance
	if hasUserTurns {
		section += "\n\n" + mixedActivityGuidance
	}
	return section + "\n\n" + activityFollowupGuidance
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
