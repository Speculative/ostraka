package store

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"ostraka/internal/models"

	"gopkg.in/yaml.v3"
)

const turnSep = "\n\n---\n"

var attributionRe = regexp.MustCompile(`^\*\*(\w+) · (.+?)\*\*$`)

type frontmatter struct {
	ID      string `yaml:"id"`
	Channel string `yaml:"channel"`
	Type    string `yaml:"type"`
	Status  string `yaml:"status"`
	Created string `yaml:"created"`
	Parent  string `yaml:"parent,omitempty"`
}

func ParseItem(path string) (models.Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return models.Item{}, err
	}

	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return models.Item{}, fmt.Errorf("missing frontmatter in %s", path)
	}

	// Find closing ---
	rest := content[4:]
	end := strings.Index(rest, "\n---\n")
	if end == -1 {
		return models.Item{}, fmt.Errorf("unclosed frontmatter in %s", path)
	}

	yamlBlock := rest[:end]
	body := strings.TrimSpace(rest[end+5:])

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return models.Item{}, fmt.Errorf("bad frontmatter in %s: %w", path, err)
	}

	created, err := time.Parse(time.RFC3339Nano, fm.Created)
	if err != nil {
		return models.Item{}, fmt.Errorf("bad created timestamp in %s: %w", path, err)
	}

	// Split body from turns
	parts := strings.Split(body, turnSep)
	itemBody := strings.TrimSpace(parts[0])

	var turns []models.Turn
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		lines := strings.SplitN(part, "\n", 2)
		if len(lines) == 0 {
			continue
		}
		m := attributionRe.FindStringSubmatch(lines[0])
		if m == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, m[2])
		if err != nil {
			continue
		}
		turnContent := ""
		if len(lines) > 1 {
			turnContent = strings.TrimSpace(lines[1])
		}
		turns = append(turns, models.Turn{
			Actor:     models.Actor(m[1]),
			Timestamp: ts,
			Content:   turnContent,
		})
	}

	itemType := models.ItemType(fm.Type)
	if itemType == "" {
		itemType = models.TypeThread
	}
	status := models.Status(fm.Status)
	if status == "" {
		status = models.StatusActive
	}

	return models.Item{
		ID:      fm.ID,
		Channel: models.Channel(fm.Channel),
		Type:    itemType,
		Status:  status,
		Created: created,
		Parent:  fm.Parent,
		Body:    itemBody,
		Turns:   turns,
	}, nil
}

func WriteItem(item models.Item, path string) error {
	fm := frontmatter{
		ID:      item.ID,
		Channel: string(item.Channel),
		Type:    string(item.Type),
		Status:  string(item.Status),
		Created: item.Created.UTC().Format(time.RFC3339Nano),
		Parent:  item.Parent,
	}

	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(yamlBytes)
	sb.WriteString("---\n")
	sb.WriteString(item.Body)

	for _, turn := range item.Turns {
		sb.WriteString(turnSep)
		sb.WriteString(fmt.Sprintf("**%s · %s**\n\n", turn.Actor, turn.Timestamp.UTC().Format(time.RFC3339Nano)))
		sb.WriteString(turn.Content)
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}
