package store

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
)

var channelDirs = map[models.Channel]string{
	models.ChannelInbox: "INBOX",
}

const archiveDir = "ARCHIVE"

const itemIDAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

var itemIDEncoding = base32.NewEncoding(itemIDAlphabet).WithPadding(base32.NoPadding)

type ListOpts struct {
	Channel *models.Channel
	Status  *models.Status
}

type Store struct {
	Root string
}

func ValidateChannel(channel models.Channel) error {
	if !models.ValidChannel(channel) {
		return fmt.Errorf("unsupported channel %q (only inbox is supported)", channel)
	}
	return nil
}

func FindRoot(start string) (string, error) {
	dir := start
	for {
		candidate := filepath.Join(dir, ".ostraka")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no .ostraka/ directory found in %s or any parent", start)
}

func NewStore(root string) (*Store, error) {
	for _, sub := range append(append(append(dirValues(), archiveDir), activityDir), partialTraceDir) {
		if err := os.MkdirAll(filepath.Join(root, sub), 0755); err != nil {
			return nil, err
		}
	}
	return &Store{Root: root}, nil
}

func dirValues() []string {
	dirs := make([]string, 0, len(channelDirs))
	for _, d := range channelDirs {
		dirs = append(dirs, d)
	}
	return dirs
}

func (s *Store) allPaths() ([]string, error) {
	var paths []string
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subdir := filepath.Join(s.Root, e.Name())
		files, err := filepath.Glob(filepath.Join(subdir, "*.md"))
		if err != nil {
			return nil, err
		}
		paths = append(paths, files...)
	}
	return paths, nil
}

func (s *Store) pathForID(id string) (string, error) {
	paths, err := s.allPaths()
	if err != nil {
		return "", err
	}
	for _, p := range paths {
		if filepath.Base(p) == id+".md" {
			return p, nil
		}
	}
	return "", fmt.Errorf("item %q not found", id)
}

func (s *Store) itemPath(item models.Item) string {
	item.Status = models.NormalizeStatus(item.Status)
	if models.TerminalStatuses[item.Status] {
		return filepath.Join(s.Root, archiveDir, item.ID+".md")
	}
	return filepath.Join(s.Root, channelDirs[item.Channel], item.ID+".md")
}

// listRetries bounds the re-scan below. One retry closes the window in
// practice; the cap is only there so a pathological writer cannot spin us.
const listRetries = 2

func (s *Store) ListItems(opts ListOpts) ([]models.Item, error) {
	if opts.Channel != nil {
		if err := ValidateChannel(*opts.Channel); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		items, unstable, err := s.listOnce(opts)
		if err != nil {
			return nil, err
		}
		// A path that existed during the directory scan but not by the time we
		// read it is an item being moved between a channel and the archive,
		// not a missing one. Re-scanning finds it at its new location; dropping
		// it would report the item as gone for one reload, which is enough to
		// lose the selection sitting on it.
		if !unstable || attempt == listRetries {
			return items, nil
		}
	}
}

func (s *Store) listOnce(opts ListOpts) (items []models.Item, unstable bool, err error) {
	paths, err := s.allPaths()
	if err != nil {
		return nil, false, err
	}
	seen := make(map[string]bool, len(paths))
	var parsed []models.Item
	for _, p := range paths {
		item, err := ParseItem(p)
		if err != nil {
			if os.IsNotExist(err) {
				unstable = true
			}
			continue
		}
		// A move writes the new location before removing the old, so an item
		// can legitimately be readable from both at once. Report it once.
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		parsed = append(parsed, item)
	}

	// Backlinks are a read-time view over the text-owned outgoing mentions.
	// Build them after parsing the complete collection so filtered listings do
	// not hide the source of a backlink.
	byMentionedID := make(map[string][]string)
	for _, item := range parsed {
		for _, mentionedID := range outgoingMentionIDs(item) {
			byMentionedID[mentionedID] = appendUnique(byMentionedID[mentionedID], item.ID)
		}
	}
	for i := range parsed {
		parsed[i].Backlinks = append([]string(nil), byMentionedID[parsed[i].ID]...)
	}

	for _, item := range parsed {

		if opts.Channel != nil && item.Channel != *opts.Channel {
			continue
		}
		if opts.Status != nil && item.Status != *opts.Status {
			continue
		}
		items = append(items, item)
	}
	// A move renames across directories, and allPaths globs them one at a
	// time — so a scan can miss an item that was in the directory already
	// globbed and arrives in one not yet globbed. Nothing fails to parse in
	// that case, so re-scan and compare: a listing that changed underneath us
	// is one we cannot trust to be complete.
	if after, err := s.allPaths(); err == nil && !sameStrings(paths, after) {
		unstable = true
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Created.Before(items[j].Created)
	})
	return items, unstable, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

func (s *Store) GetItem(id string) (models.Item, error) {
	path, err := s.pathForID(id)
	if err != nil {
		return models.Item{}, err
	}
	return ParseItem(path)
}

// RenameItem changes an item's single-line title without changing its body,
// turns, or filesystem location.
func (s *Store) RenameItem(id, title string) (models.Item, error) {
	if err := ValidateTitle(title); err != nil {
		return models.Item{}, err
	}
	path, err := s.pathForID(id)
	if err != nil {
		return models.Item{}, err
	}
	item, err := ParseItem(path)
	if err != nil {
		return models.Item{}, err
	}
	item.Title = strings.TrimSpace(title)
	if err := WriteItem(item, path); err != nil {
		return models.Item{}, err
	}
	return item, nil
}

func (s *Store) CreateItem(channel models.Channel, title, body string, itemType models.ItemType, status models.Status, parent string) (models.Item, error) {
	status = models.NormalizeStatus(status)
	if err := ValidateChannel(channel); err != nil {
		return models.Item{}, err
	}
	if err := ValidateTitle(title); err != nil {
		return models.Item{}, err
	}
	var parentItem models.Item
	if parent != "" {
		var err error
		parentItem, err = s.GetItem(parent)
		if err != nil {
			return models.Item{}, fmt.Errorf("parent %q: %w", parent, err)
		}
		if parentItem.Parent != "" {
			return models.Item{}, fmt.Errorf("parent %q is a subthread; subthreads cannot be nested", parent)
		}
		if models.TerminalStatuses[parentItem.Status] {
			return models.Item{}, fmt.Errorf("cannot create a subthread under terminal item %q", parent)
		}
	}
	// Check collisions across the complete namespace so live and archived
	// items, including legacy timestamp IDs, can coexist safely.
	paths, err := s.allPaths()
	if err != nil {
		return models.Item{}, err
	}
	existing := make(map[string]bool, len(paths))
	for _, p := range paths {
		existing[filepath.Base(p[:len(p)-3])] = true // strip .md
	}

	now := time.Now().UTC()
	item := models.Item{
		Channel: channel,
		Type:    itemType,
		Status:  status,
		Created: now,
		Parent:  parent,
		Title:   strings.TrimSpace(title),
		Body:    body,
	}
	item.Mentions = itemMentionIDs(item.Body, nil)
	for {
		id, err := newItemID()
		if err != nil {
			return models.Item{}, err
		}
		if existing[id] {
			continue
		}
		release, acquired, err := s.reserveItemID(id)
		if err != nil {
			return models.Item{}, err
		}
		if !acquired {
			continue
		}
		item.ID = id
		err = WriteItem(item, s.itemPath(item))
		release()
		if err != nil {
			return models.Item{}, err
		}
		break
	}
	if parent == "" && item.Status == models.StatusBacklog {
		if err := s.addBacklogRoot(item.ID); err != nil {
			return models.Item{}, err
		}
	}
	if parent != "" {
		if err := s.addSubthreadActivity(parentItem, item, ActivitySubthreadCreated, ""); err != nil {
			return models.Item{}, err
		}
	}
	return item, nil
}

func newItemID() (string, error) {
	// Five bytes encode to eight base32 symbols: 40 bits of project-local
	// entropy. Grouping the symbols makes IDs easier to scan and type.
	var random [5]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate item ID: %w", err)
	}
	encoded := itemIDEncoding.EncodeToString(random[:])
	return encoded[:4] + "-" + encoded[4:], nil
}

// reserveItemID prevents concurrent creators from publishing the same ID in
// different item directories. A reservation left by a crash only removes one
// random value from the 40-bit namespace; later creations simply choose again.
func (s *Store) reserveItemID(id string) (release func(), acquired bool, err error) {
	path := filepath.Join(s.Root, ".item-id-"+id)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, false, err
	}
	return func() { _ = os.Remove(path) }, true, nil
}

// CreateSubthread accepts either a root or one of its children and always
// attaches the new item directly to the root. This is the store-level guard
// behind the product's one-level tree invariant.
func (s *Store) CreateSubthread(contextID string, title, body string, itemType models.ItemType, status models.Status) (models.Item, error) {
	context, err := s.GetItem(contextID)
	if err != nil {
		return models.Item{}, err
	}
	rootID := context.ID
	if context.Parent != "" {
		rootID = context.Parent
	}
	return s.CreateItem(context.Channel, title, body, itemType, status, rootID)
}

// AddMention creates a directed mention from firstID to secondID by
// appending @secondID to the first item's text. Only the mentioner is written,
// so reparenting or editing either endpoint cannot leave a reciprocal edge
// index out of sync.
func (s *Store) AddMention(firstID, secondID string) (models.Item, error) {
	first, err := s.GetItem(firstID)
	if err != nil {
		return models.Item{}, err
	}
	second, err := s.GetItem(secondID)
	if err != nil {
		return models.Item{}, err
	}
	if first.ID == second.ID {
		return models.Item{}, fmt.Errorf("an item cannot be related to itself")
	}
	if !contains(first.Mentions, second.ID) {
		first.Body = appendMention(first.Body, second.ID)
		first.Mentions = MentionIDs(first.Body)
	}
	if err := WriteItem(first, s.itemPath(first)); err != nil {
		return models.Item{}, err
	}
	return first, nil
}

// AddRelated is the compatibility name used by the existing CLI and TUI.
// New code should prefer AddMention, which makes the direction explicit.
func (s *Store) AddRelated(firstID, secondID string) (models.Item, error) {
	return s.AddMention(firstID, secondID)
}

// MentionedItems returns the items named by id's outgoing @mentions. Legacy
// related frontmatter is included as a compatibility source of outgoing IDs.
func (s *Store) MentionedItems(id string) ([]models.Item, error) {
	item, err := s.GetItem(id)
	if err != nil {
		return nil, err
	}
	all, err := s.ListItems(ListOpts{})
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool)
	for _, mentionedID := range outgoingMentionIDs(item) {
		ids[mentionedID] = true
	}
	return itemsWithIDs(all, ids), nil
}

// BacklinkItems returns items whose text mentions id. It scans the item files
// at read time, so deleting an item leaves harmless dangling text rather than
// requiring cleanup of a maintained reverse index.
func (s *Store) BacklinkItems(id string) ([]models.Item, error) {
	if _, err := s.GetItem(id); err != nil {
		return nil, err
	}
	all, err := s.ListItems(ListOpts{})
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool)
	for _, candidate := range all {
		if contains(outgoingMentionIDs(candidate), id) {
			ids[candidate.ID] = true
		}
	}
	return itemsWithIDs(all, ids), nil
}

// RelatedItems preserves the old symmetric traversal API by returning the
// union of outgoing mentions and backlinks. New callers that care about
// presentation direction should use MentionedItems and BacklinkItems.
func (s *Store) RelatedItems(id string) ([]models.Item, error) {
	if _, err := s.GetItem(id); err != nil {
		return nil, err
	}
	all, err := s.ListItems(ListOpts{})
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool)
	item, _ := s.GetItem(id)
	for _, related := range outgoingMentionIDs(item) {
		ids[related] = true
	}
	for _, candidate := range all {
		if contains(outgoingMentionIDs(candidate), id) {
			ids[candidate.ID] = true
		}
	}
	return itemsWithIDs(all, ids), nil
}

func outgoingMentionIDs(item models.Item) []string {
	ids := append([]string(nil), item.Mentions...)
	for _, legacyID := range item.Related {
		ids = appendUnique(ids, legacyID)
	}
	return ids
}

func itemsWithIDs(items []models.Item, ids map[string]bool) []models.Item {
	var out []models.Item
	for _, item := range items {
		if ids[item.ID] {
			out = append(out, item)
		}
	}
	return out
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func appendMention(body, id string) string {
	mention := "@" + id
	if strings.TrimSpace(body) == "" {
		return mention
	}
	return strings.TrimRight(body, "\n") + "\n\n" + mention
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (s *Store) AddTurn(id string, actor models.Actor, content string) (models.Item, error) {
	path, err := s.pathForID(id)
	if err != nil {
		return models.Item{}, err
	}
	item, err := ParseItem(path)
	if err != nil {
		return models.Item{}, err
	}
	before := item
	item.Turns = append(item.Turns, models.Turn{
		Actor:     actor,
		Timestamp: time.Now().UTC(),
		Content:   content,
	})
	item.Mentions = itemMentionIDs(item.Body, item.Turns)
	if actor == models.ActorAgent {
		if next, ok := StatusAfterAgentTurn(item.Status); ok {
			item.Status = next
		}
	}
	// A status change can move the file between channel and archive
	// directories, so re-derive the path rather than writing to the one the
	// item was read from.
	newPath := s.itemPath(item)
	if err := WriteItem(item, newPath); err != nil {
		return models.Item{}, err
	}
	if newPath != path {
		// Write the new location before dropping the old one. The reverse order
		// leaves a window where the item exists nowhere, and a concurrent reload
		// would show it as deleted.
		if err := os.Remove(path); err != nil {
			return models.Item{}, err
		}
	}
	if before.Status != item.Status {
		if err := s.reconcileBacklogMembership(before, item); err != nil {
			return models.Item{}, err
		}
	}
	return item, nil
}

// StatusAfterAgentTurn gives the status an item moves to once the agent has
// spoken, and whether it moves at all. An agent turn hands the ball back to
// the user, mirroring the advance to pending-agent that a user turn triggers;
// without it an item stays flagged for the agent after it has already been
// answered and keeps resurfacing in the pending-agent queue.
//
// Backlog and the terminal statuses are left alone: those are parked states,
// and an agent turn is not a request for the user to do anything.
func StatusAfterAgentTurn(current models.Status) (models.Status, bool) {
	switch current {
	case models.StatusActive, models.StatusPendingAgent, models.StatusAgentAcknowledged:
		return models.StatusPendingUser, true
	}
	return current, false
}

func (s *Store) SetStatus(id string, status models.Status) (models.Item, error) {
	return s.SetStatusBy(id, status, models.ActorUser)
}

func (s *Store) SetStatusBy(id string, status models.Status, actor models.Actor) (models.Item, error) {
	status = models.NormalizeStatus(status)
	oldPath, err := s.pathForID(id)
	if err != nil {
		return models.Item{}, err
	}
	item, err := ParseItem(oldPath)
	if err != nil {
		return models.Item{}, err
	}
	if models.TerminalStatuses[status] && item.Parent == "" {
		children, err := s.ListItems(ListOpts{})
		if err != nil {
			return models.Item{}, err
		}
		for _, child := range children {
			if child.Parent == item.ID && !models.TerminalStatuses[child.Status] {
				return models.Item{}, fmt.Errorf("cannot close root %q while subthread %q is still open", item.ID, child.ID)
			}
		}
	}
	oldStatus := item.Status
	item.Status = status
	newPath := s.itemPath(item)
	if err := WriteItem(item, newPath); err != nil {
		return models.Item{}, err
	}
	// Only after the new file exists, so the item is never absent from both
	// locations at once. A reader that lands in between sees it twice, which
	// is harmless — a reader that saw it in neither would show it as gone.
	if newPath != oldPath {
		if err := os.Remove(oldPath); err != nil {
			return models.Item{}, err
		}
	}
	if item.Parent != "" && !models.TerminalStatuses[oldStatus] && models.TerminalStatuses[status] {
		root, rootErr := s.GetItem(item.Parent)
		if rootErr != nil {
			return models.Item{}, rootErr
		}
		if err := s.appendActivity(root.ID, models.Activity{
			ID:         activityID(time.Now().UTC()),
			Type:       ActivitySubthreadClosed,
			ChildID:    item.ID,
			ChildTitle: item.Title,
			Result:     string(status),
			Actor:      actor,
			Timestamp:  time.Now().UTC(),
		}); err != nil {
			return models.Item{}, err
		}
	}
	if err := s.reconcileBacklogMembership(models.Item{ID: item.ID, Parent: item.Parent, Status: oldStatus}, item); err != nil {
		return models.Item{}, err
	}
	return item, nil
}

func (s *Store) DeleteItem(id string) error {
	path, err := s.pathForID(id)
	if err != nil {
		return err
	}
	deleted, err := ParseItem(path)
	if err != nil {
		return err
	}
	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Parent == id {
			return fmt.Errorf("cannot delete item %q while it has subthreads", id)
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := s.deletePartialTraces(id); err != nil {
		return err
	}
	if deleted.Parent == "" {
		if err := s.removeBacklogRoot(deleted.ID); err != nil {
			return err
		}
	}
	return nil
}
