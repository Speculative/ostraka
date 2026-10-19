package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
