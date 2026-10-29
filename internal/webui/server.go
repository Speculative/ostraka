// Package webui exposes the shared Ostraka store to a local browser workspace.
package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/reviewdiff"
	"github.com/Speculative/ostraka/internal/store"
	"github.com/Speculative/ostraka/internal/supervisor"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

//go:embed static/*
var assets embed.FS

type Server struct {
	Store       *store.Store
	Supervisor  *supervisor.Supervisor
	AllowedHost string
}

// Handler accepts only same-origin writes and never enables CORS. The listener
// should bind to loopback unless protected by an authenticated reverse proxy.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/items/{id}", s.item)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/action", s.action)
	mux.HandleFunc("POST /api/highlight", s.highlight)
	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		p := supervisor.Provider(r.URL.Query().Get("provider"))
		if p != supervisor.ProviderClaude && p != supervisor.ProviderCodex {
			fail(w, 400, fmt.Errorf("unknown provider"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var v []supervisor.ModelOption
		var e error
		if s.Supervisor != nil {
			v, e = s.Supervisor.AvailableModels(ctx, p)
		} else {
			v, e = supervisor.DiscoverModels(ctx, p)
		}
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/diff", func(w http.ResponseWriter, r *http.Request) {
		v, e := reviewdiff.Capture(r.Context(), filepath.Dir(s.Store.Root))
		respond(w, v, e)
	})
	sub, _ := fs.Sub(assets, "static")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		if host != "localhost" && net.ParseIP(host) == nil && host != s.AllowedHost {
			fail(w, 403, fmt.Errorf("unrecognized host"))
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				fail(w, 403, fmt.Errorf("cross-origin request rejected"))
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403, fmt.Errorf("cross-site request rejected"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func fail(w http.ResponseWriter, code int, e error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
}
func respond(w http.ResponseWriter, v any, e error) {
	if e != nil {
		fail(w, 400, e)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	items, e := s.Store.ListItems(store.ListOpts{})
	if e != nil {
		fail(w, 500, e)
		return
	}
	reviews, e := s.Store.ListReviews()
	if e != nil {
		fail(w, 500, e)
		return
	}
	instructions, e := s.Store.ProjectInstructions()
	if e != nil {
		fail(w, 500, e)
		return
	}
	brief, e := s.Store.ProjectBrief()
	if e != nil {
		fail(w, 500, e)
		return
	}
	backlog, e := s.Store.BacklogOrderFor(items)
	if e != nil {
		fail(w, 500, e)
		return
	}
	orders, e := s.Store.ChildOrdersFor(items)
	if e != nil {
		fail(w, 500, e)
		return
	}
	paused := false
	if s.Supervisor != nil {
		paused = s.Supervisor.DispatchesPaused()
	}
	respond(w, map[string]any{"items": items, "reviews": reviews, "instructions": instructions, "brief": brief, "backlog_order": backlog, "child_orders": orders, "paused": paused, "supervised": s.Supervisor != nil, "project": filepath.Base(filepath.Dir(s.Store.Root))}, nil)
}

var htmlPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^syntax-(comment|keyword|string|number|literal|name|operator|plain)$`)).OnElements("span")
	return p
}()
var markdownRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(codeRenderer{}, 100))))

func markdown(content string) string {
	var b bytes.Buffer
	if markdownRenderer.Convert([]byte(content), &b) != nil {
		return ""
	}
	return htmlPolicy.Sanitize(b.String())
}
func (s *Server) item(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, e := s.Store.GetItem(id)
	if e != nil {
		fail(w, 404, e)
		return
	}
	activities, e := s.Store.ListActivities(id)
	if e != nil {
		fail(w, 500, e)
		return
	}
	partials, e := s.Store.ListPartialTraces(id)
	if e != nil {
		fail(w, 500, e)
		return
	}
	draft, e := s.Store.LoadDraft(id)
	if e != nil {
		fail(w, 500, e)
		return
	}
	live, active := supervisor.ReadLiveState(s.Store.Root, id)
	if item.Status != models.StatusAgentAcknowledged {
		live = ""
		active = false
	}
	turnsHTML := make([]string, len(item.Turns))
	for i, t := range item.Turns {
		turnsHTML[i] = markdown(t.Content)
	}
	snapshot, sessionErr := supervisor.ReadSessionSnapshot(s.Store.Root, id)
	session := map[string]any{"provider": snapshot.Provider, "model": snapshot.Model, "effort": snapshot.Effort, "stale": snapshot.Stale, "current": snapshot.Current}
	sessionWarning := ""
	if sessionErr != nil {
		sessionWarning = sessionErr.Error()
	}
	if snapshot.Current == nil {
		for i := len(activities) - 1; i >= 0; i-- {
			a := activities[i]
			if a.Type == store.ActivityAgentSessionStarted {
				session["current"] = supervisor.ModelSelection{Provider: supervisor.Provider(a.Result), Model: a.Model, Effort: a.Effort}
				break
			}
		}
	}
	dispatchError := supervisor.ReadDispatchError(s.Store.Root, id)
	if s.Supervisor != nil {
		dispatchError, _ = s.Supervisor.DispatchError(id)
	}
	respond(w, map[string]any{"item": item, "activities": activities, "partials": partials, "live": live, "live_active": active, "draft": draft, "body_html": markdown(item.Body), "turns_html": turnsHTML, "session": session, "session_warning": sessionWarning, "error": dispatchError}, nil)
}

// A metadata fingerprint observes atomic replacements and external CLI/TUI
// writes without consuming fsnotify descriptors for every browser connection.
func (s *Server) fingerprint() string {
	h := sha256.New()
	_ = filepath.WalkDir(s.Store.Root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			if info, e := d.Info(); e == nil {
				fmt.Fprintf(h, "%s:%d:%d\n", path, info.Size(), info.ModTime().UnixNano())
			}
		}
		return nil
	})
	if s.Supervisor != nil {
		fmt.Fprint(h, s.Supervisor.DispatchesPaused())
	}
	s.workingTreeFingerprint(h)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Include Git changes even when no item or review file changes. Porcelain
// status names changed/untracked files; their metadata detects further edits
// while their status stays unchanged. HEAD also invalidates after a commit.
func (s *Server) workingTreeFingerprint(h io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dir := filepath.Dir(s.Store.Root)
	revision := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel", "HEAD")
	revision.Dir = dir
	revision.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, _ := revision.Output() // Unborn repositories still print their root.
	root, _, found := strings.Cut(string(out), "\n")
	if !found {
		return
	}
	fmt.Fprint(h, string(out))
	status := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	status.Dir = dir
	status.Env = revision.Env
	out, err := status.Output()
	if err != nil {
		return
	}
	fmt.Fprint(h, string(out))
	for _, entry := range strings.Split(string(out), "\x00") {
		if len(entry) < 4 {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, entry[3:])); err == nil {
			fmt.Fprintf(h, "%s:%d:%d\n", entry[3:], info.Size(), info.ModTime().UnixNano())
		}
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, fmt.Errorf("streaming unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	last := ""
	tick := time.NewTicker(750 * time.Millisecond)
	defer tick.Stop()
	send := func() bool {
		next := s.fingerprint()
		if next != last {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, e := fmt.Fprintf(w, "event: change\ndata: %s\n\n", next); e != nil {
				return false
			}
			f.Flush()
			last = next
		}
		return true
	}
	if !send() {
		return
	}
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if !send() {
				return
			}
		case <-heartbeat.C:
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, e := fmt.Fprint(w, ": heartbeat\n\n"); e != nil {
				return
			}
			f.Flush()
		}
	}
}
