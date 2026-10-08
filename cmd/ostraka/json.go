package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func itemsToJSON(items []models.Item, titles map[string]string, activities map[string][]models.Activity) ([]map[string]any, error) {
	out := make([]map[string]any, len(items))
	for i, item := range items {
		var err error
		out[i], err = itemToJSONWithConversation(item, activities[item.ID], nil, titles)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func itemTitles(items []models.Item) map[string]string {
	titles := make(map[string]string, len(items))
	for _, item := range items {
		titles[item.ID] = item.Title
	}
	return titles
}

func itemLinks(ids []string, titles map[string]string) []map[string]any {
	links := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		var title any
		if name, ok := titles[id]; ok {
			title = name
		}
		links = append(links, map[string]any{"id": id, "title": title})
	}
	return links
}

type jsonConversationEvent struct {
	timestamp time.Time
	entry     map[string]any
}

func partialTraceJSON(trace models.PartialTrace) map[string]any {
	return map[string]any{
		"id":         trace.ID,
		"started_at": trace.Timestamp.Format(time.RFC3339Nano),
		"status":     trace.Status,
		"output":     trace.Content,
	}
}

func activityJSON(activity models.Activity) map[string]any {
	out := map[string]any{
		"id":      activity.ID,
		"type":    activity.Type,
		"actor":   activity.Actor,
		"handled": activity.Handled,
	}
	for key, value := range map[string]string{
		"child_id": activity.ChildID, "child_title": activity.ChildTitle,
		"item_id": activity.ItemID, "item_title": activity.ItemTitle,
		"previous_title": activity.PreviousTitle,
		"group":          activity.Group, "previous_group": activity.PreviousGroup,
		"from_root_id": activity.FromRootID, "to_root_id": activity.ToRootID,
		"model": activity.Model, "effort": activity.Effort,
	} {
		if value != "" {
			out[key] = value
		}
	}
	if activity.Type == store.ActivityAgentSessionStarted {
		out["provider"] = activity.Result
	} else if activity.Result != "" {
		out["result"] = activity.Result
	}
	return out
}

func itemToJSONWithConversation(item models.Item, activities []models.Activity, partials []models.PartialTrace, titles map[string]string) (map[string]any, error) {
	events := make([]jsonConversationEvent, 0, len(item.Turns)+len(activities)+len(partials))
	turnEntries := make([]map[string]any, len(item.Turns))
	for i, turn := range item.Turns {
		entry := map[string]any{"timestamp": turn.Timestamp.Format(time.RFC3339Nano)}
		switch turn.Actor {
		case models.ActorUser:
			entry["user_message"] = turn.Content
		case models.ActorAgent:
			entry["agent_reply"] = turn.Content
		default:
			return nil, fmt.Errorf("unknown turn actor %q", turn.Actor)
		}
		turnEntries[i] = entry
		events = append(events, jsonConversationEvent{turn.Timestamp, entry})
	}
	for _, activity := range activities {
		entry := map[string]any{
			"timestamp": activity.Timestamp.Format(time.RFC3339Nano),
			"activity":  activityJSON(activity),
		}
		events = append(events, jsonConversationEvent{activity.Timestamp, entry})
	}
	for _, trace := range partials {
		if trace.TurnTimestamp.IsZero() {
			entry := map[string]any{
				"timestamp":     trace.Timestamp.Format(time.RFC3339Nano),
				"partial_trace": partialTraceJSON(trace),
			}
			events = append(events, jsonConversationEvent{trace.Timestamp, entry})
			continue
		}
		matched := false
		for i, turn := range item.Turns {
			if turn.Actor != models.ActorAgent || !turn.Timestamp.Equal(trace.TurnTimestamp) {
				continue
			}
			if _, exists := turnEntries[i]["partial_trace"]; exists {
				return nil, fmt.Errorf("multiple partial traces linked to turn %s", trace.TurnTimestamp.Format(time.RFC3339Nano))
			}
			turnEntries[i]["partial_trace"] = partialTraceJSON(trace)
			matched = true
			break
		}
		if !matched {
			return nil, fmt.Errorf("partial trace %s links to missing agent turn %s", trace.ID, trace.TurnTimestamp.Format(time.RFC3339Nano))
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].timestamp.Before(events[j].timestamp) })
	conversation := make([]map[string]any, len(events))
	for i, event := range events {
		conversation[i] = event.entry
	}
	return map[string]any{
		"id":              item.ID,
		"channel":         item.Channel,
		"type":            item.Type,
		"status":          item.Status,
		"mode":            models.NormalizeAgentMode(item.Mode),
		"created":         item.Created.Format(time.RFC3339Nano),
		"parent":          item.Parent,
		"group":           item.Group,
		"mentioned_items": itemLinks(item.Mentions, titles),
		"backlinks":       itemLinks(item.Backlinks, titles),
		"title":           item.Title,
		"body":            item.Body,
		"conversation":    conversation,
	}, nil
}
