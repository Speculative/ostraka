package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	st, e := store.NewStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return &Server{Store: st}
}
func request(t *testing.T, s *Server, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "http://localhost/api/action", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestBrowserWritesUseStoreAndUserAttribution(t *testing.T) {
	s := testServer(t)
	w := request(t, s, `{"op":"create","title":"Browser item","body":"Description"}`, "http://localhost")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var item models.Item
	if e := json.Unmarshal(w.Body.Bytes(), &item); e != nil {
		t.Fatal(e)
	}
	w = request(t, s, `{"op":"turn","id":"`+item.ID+`","content":"Please implement"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	got, e := s.Store.GetItem(item.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Status != models.StatusPendingAgent || len(got.Turns) != 1 || got.Turns[0].Actor != models.ActorUser {
		t.Fatalf("unexpected item: %+v", got)
	}
	w = request(t, s, `{"op":"status","id":"`+item.ID+`","status":"agent-acknowledged"}`, "")
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestBacklogTurnsStayParkedAndGoalGuardsApply(t *testing.T) {
	s := testServer(t)
	item, e := s.Store.CreateItem(models.ChannelInbox, "Parked", "", models.TypeThread, models.StatusBacklog, "")
	if e != nil {
		t.Fatal(e)
	}
	w := request(t, s, `{"op":"turn","id":"`+item.ID+`","content":"later"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	got, _ := s.Store.GetItem(item.ID)
	if got.Status != models.StatusBacklog {
		t.Fatal(got.Status)
	}
	if _, e := s.Store.SetStatus(item.ID, models.StatusActive); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Store.SetMode(item.ID, models.AgentModeGoal); e != nil {
		t.Fatal(e)
	}
	w = request(t, s, `{"op":"delete","id":"`+item.ID+`"}`, "")
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestOriginAndJSONBoundary(t *testing.T) {
	s := testServer(t)
	for _, origin := range []string{"https://attacker.example", "null", "http://localhost.attacker.example"} {
		w := request(t, s, `{"op":"create","title":"x"}`, origin)
		if w.Code != 403 {
			t.Fatalf("%s: %d", origin, w.Code)
		}
	}
	for _, body := range []string{`{"op":"create","title":"x","unknown":true}`, `{"op":"pause"} {}`, `{"op":"shell"}`} {
		if w := request(t, s, body, ""); w.Code != 400 {
			t.Fatalf("%s: %d", body, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost/api/action", strings.NewReader(`{"op":"pause"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
}
func TestDetailSanitizesMarkdownAndReadsExternalChanges(t *testing.T) {
	s := testServer(t)
	item, e := s.Store.CreateItem(models.ChannelInbox, "Safe", "# Heading\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))", models.TypeThread, models.StatusActive, "")
	if e != nil {
		t.Fatal(e)
	}
	before := s.fingerprint()
	if _, e := s.Store.RenameItem(item.ID, "Updated externally"); e != nil {
		t.Fatal(e)
	}
	if before == s.fingerprint() {
		t.Fatal("missed external write")
	}
	r := httptest.NewRequest(http.MethodGet, "http://localhost/api/items/"+item.ID, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	html := out["body_html"].(string)
	if !strings.Contains(html, "<h1>Heading</h1>") || strings.Contains(html, "<script") || strings.Contains(html, "javascript:") {
		t.Fatal(html)
	}
}
func TestEventsImmediatelyInvalidate(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	resp, e := http.Get(server.URL + "/api/events")
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b := make([]byte, 14)
	if _, e := resp.Body.Read(b); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(string(b), "event: change") {
		t.Fatal(string(b))
	}
}

func TestDNSRebindingHostRejected(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "http://attacker.example/api/state", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestAttachedDetailReadsOperationalStateWithoutStartingSupervisor(t *testing.T) {
	s := testServer(t)
	item, e := s.Store.CreateItem(models.ChannelInbox, "Attached", "", models.TypeThread, models.StatusActive, "")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(s.Store.Root, "supervisor")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	session := `{"sessions":{"` + item.ID + `":{"provider":"codex","model":"example-model","effort":"high","session_id":"secret-session"}}}`
	for name, content := range map[string]string{"session.json": session, "error-" + item.ID + ".txt": "Authentication needed", "live-" + item.ID + ".txt": "still running"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest("GET", "http://localhost/api/items/"+item.ID, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["error"] != "Authentication needed" || out["session"].(map[string]any)["model"] != "example-model" {
		t.Fatal(out)
	}
	if strings.Contains(w.Body.String(), "secret-session") {
		t.Fatal("provider conversation ID leaked")
	}
	b, e := os.ReadFile(filepath.Join(dir, "live-"+item.ID+".txt"))
	if e != nil || string(b) != "still running" {
		t.Fatal("attached read changed live state", e)
	}
}
func TestMarkdownRendersTables(t *testing.T) {
	html := markdown("| Layout | Width |\n| --- | --- |\n| Mobile | narrow |")
	if !strings.Contains(html, "<table>") || !strings.Contains(html, "<td>Mobile</td>") {
		t.Fatal(html)
	}
}

func TestLiveEventsReachEveryClientForExternalAndBrowserWrites(t *testing.T) {
	s := testServer(t)
	item, err := s.Store.CreateItem(models.ChannelInbox, "Before", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	readers := make([]*bufio.Reader, 2)
	for i := range readers {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		readers[i] = bufio.NewReader(resp.Body)
	}
	readChange := func(reader *bufio.Reader) string {
		t.Helper()
		var data string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			}
			if line == "\n" && data != "" {
				return data
			}
		}
	}
	first := []string{readChange(readers[0]), readChange(readers[1])}
	// A separate store instance exercises the same disk writes used by CLI/TUI.
	external, err := store.NewStore(s.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := external.RenameItem(item.ID, "From TUI"); err != nil {
		t.Fatal(err)
	}
	for i, reader := range readers {
		next := readChange(reader)
		if next == first[i] {
			t.Fatal("client missed external write")
		}
		first[i] = next
	}
	body := `{"op":"rename","id":"` + item.ID + `","title":"From browser"}`
	resp, err := http.Post(server.URL+"/api/action", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.Status)
	}
	for i, reader := range readers {
		next := readChange(reader)
		if next == first[i] {
			t.Fatal("client missed browser write")
		}
		first[i] = next
	}
	if err := external.SaveDraft(item.ID, "Shared draft"); err != nil {
		t.Fatal(err)
	}
	for i, reader := range readers {
		if readChange(reader) == first[i] {
			t.Fatal("client missed draft write")
		}
	}
	got, err := external.GetItem(item.ID)
	if err != nil || got.Title != "From browser" {
		t.Fatalf("external store: %+v, %v", got, err)
	}
}

func TestWorkingTreeEditsInvalidateWithoutItemWrites(t *testing.T) {
	project := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s: %v", out, err)
		}
	}
	runGit("init")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")
	st, err := store.NewStore(filepath.Join(project, ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(project, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".ostraka/\n")
	write("file with spaces.txt", "original\n")
	runGit("add", ".")
	runGit("commit", "-m", "Initial")
	before := s.fingerprint()
	write("file with spaces.txt", "changed\n")
	changed := s.fingerprint()
	if before == changed {
		t.Fatal("missed working tree edit")
	}
	// Status remains M, and content has the same size. Advance the timestamp
	// explicitly so this assertion is independent of filesystem clock precision.
	write("file with spaces.txt", "another\n")
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(filepath.Join(project, "file with spaces.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	if changed == s.fingerprint() {
		t.Fatal("missed edit to already modified file")
	}
	before = s.fingerprint()
	write("new file.txt", "untracked\n")
	if before == s.fingerprint() {
		t.Fatal("missed untracked file")
	}
	runGit("add", ".")
	before = s.fingerprint()
	runGit("commit", "-m", "Changes")
	if before == s.fingerprint() {
		t.Fatal("missed commit")
	}
}

func TestAttachedBrowserCanChangeNextModelWithoutQueuing(t *testing.T) {
	s := testServer(t)
	item, err := s.Store.CreateItem(models.ChannelInbox, "Model settings", "", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	before := s.fingerprint()
	w := request(t, s, `{"op":"session","id":"`+item.ID+`","provider":"codex","model":"next-model","effort":"high"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if s.fingerprint() == before {
		t.Fatal("model change did not invalidate clients")
	}
	got, err := s.Store.GetItem(item.ID)
	if err != nil || got.Status != models.StatusBacklog || len(got.Turns) != 0 {
		t.Fatalf("selection queued work: %+v, %v", got, err)
	}
	r := httptest.NewRequest("GET", "http://localhost/api/items/"+item.ID, nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var detail struct {
		Session struct{ Provider, Model, Effort string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Session.Model != "next-model" || detail.Session.Provider != "codex" || detail.Session.Effort != "high" {
		t.Fatalf("detail: %+v", detail)
	}
	w = request(t, s, `{"op":"session","id":"`+item.ID+`","provider":"unknown","model":"bad"}`, "")
	if w.Code != 400 {
		t.Fatal("unknown provider accepted")
	}
}
