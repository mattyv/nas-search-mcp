package editor

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// inputError is a problem with what the person typed, shown back to them.
type inputError string

func (e inputError) Error() string { return string(e) }

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *server) parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		if bodyTooLarge(err) {
			s.problem(w, r, http.StatusRequestEntityTooLarge, "That is too large to save (64 KiB limit).")
		} else {
			s.problem(w, r, http.StatusBadRequest, "The form could not be read.")
		}
		return false
	}
	return true
}

func projectPath(p string) string { return "/p/" + url.PathEscape(p) }

// ---- 1. Projects ----

func (s *server) projects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.Projects(r.Context(), s.roots)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, 200, "projects", page{Title: "Projects", Nav: "projects", Data: map[string]any{"Projects": ps}})
}

// ---- 2. Project page ----

func (s *server) project(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	pkg, err := s.store.OpenContext(r.Context(), s.roots, memoryapi.ContextRequest{Project: name, Intent: "review", Budget: contextBudget})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, 200, "project", page{Title: name, Nav: "projects", Data: map[string]any{"Project": name, "Ctx": pkg}})
}

// ---- 3. Checkpoint ----

type checkpointFields struct{ Summary, Goal, Done, Decisions, Blockers, Next, Branch, Commit string }

func fieldsOf(c *memoryapi.Checkpoint) checkpointFields {
	if c == nil {
		return checkpointFields{}
	}
	return checkpointFields{c.Summary, c.Goal, joinLines(c.Done), joinLines(c.Decisions), joinLines(c.Blockers), joinLines(c.Next), c.Branch, c.Commit}
}

func (s *server) checkpointForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	cp, err := s.store.LatestCheckpoint(r.Context(), s.roots, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if cp == nil {
		ps, err := s.store.Projects(r.Context(), s.roots)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		known := false
		for _, p := range ps {
			known = known || p.Project == name
		}
		if !known {
			s.fail(w, r, memoryapi.ErrNotFound)
			return
		}
	}
	version := 0
	if cp != nil {
		version = cp.Version
	}
	s.render(w, r, 200, "checkpoint", page{Title: "Edit checkpoint", Nav: "projects",
		Data: map[string]any{"Project": name, "Version": version, "Form": fieldsOf(cp)}})
}

func (s *server) checkpointSave(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if !s.parseForm(w, r) {
		return
	}
	f := checkpointFields{
		Summary: r.PostFormValue("summary"), Goal: r.PostFormValue("goal"), Done: r.PostFormValue("done"),
		Decisions: r.PostFormValue("decisions"), Blockers: r.PostFormValue("blockers"), Next: r.PostFormValue("next"),
		Branch: r.PostFormValue("branch"), Commit: r.PostFormValue("commit"),
	}
	expected, err := strconv.Atoi(r.PostFormValue("expected_version"))
	if err != nil || expected < 0 {
		s.problem(w, r, http.StatusBadRequest, "The form is missing its version. Reload the page and try again.")
		return
	}
	redo := func(code int, version int, msg string, newer *memoryapi.Checkpoint) {
		s.render(w, r, code, "checkpoint", page{Title: "Edit checkpoint", Nav: "projects", Err: msg,
			Data: map[string]any{"Project": name, "Version": version, "Form": f, "Newer": newer}})
	}
	if !nonBlank(f.Summary) {
		redo(http.StatusBadRequest, expected, "Add a summary before saving.", nil)
		return
	}
	latest, err := s.store.LatestCheckpoint(r.Context(), s.roots, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	scope := s.cfg.DefaultScope
	if latest != nil {
		scope = latest.Scope
	}
	_, err = s.store.SaveCheckpoint(r.Context(), s.roots, memoryapi.Checkpoint{
		Project: name, Scope: scope, Goal: strings.TrimSpace(f.Goal), Summary: strings.TrimSpace(f.Summary),
		Branch: strings.TrimSpace(f.Branch), Commit: strings.TrimSpace(f.Commit),
		Done: splitLines(f.Done), Decisions: splitLines(f.Decisions), Blockers: splitLines(f.Blockers), Next: splitLines(f.Next),
		Client: clientName, Actor: memoryapi.ActorUser,
	}, expected)
	if errors.Is(err, memoryapi.ErrConflict) {
		newer, lerr := s.store.LatestCheckpoint(r.Context(), s.roots, name)
		if lerr != nil || newer == nil {
			s.fail(w, r, err)
			return
		}
		redo(http.StatusConflict, newer.Version, "This checkpoint changed elsewhere while you were editing. Your text is kept below. Compare it with the newer version, then save again.", newer)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, projectPath(name)+"?done=saved")
}

// ---- 4. Timeline ----

type dayGroup struct {
	Day    string
	Events []memoryapi.Event
}

const dayLayout = "2006-01-02"

func (s *server) timeline(w http.ResponseWriter, r *http.Request) {
	day := time.Now().UTC().Truncate(24 * time.Hour)
	if v := r.URL.Query().Get("day"); v != "" {
		d, err := time.Parse(dayLayout, v)
		if err != nil {
			s.problem(w, r, http.StatusBadRequest, "That date is not valid. Use YYYY-MM-DD.")
			return
		}
		day = d
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	evs, err := s.store.SearchEvents(r.Context(), s.roots, memoryapi.EventQuery{Project: project, From: day, To: day.Add(24 * time.Hour), Limit: 200})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var groups []dayGroup
	for _, e := range evs {
		d := e.OccurredAt.UTC().Format(dayLayout)
		if n := len(groups); n == 0 || groups[n-1].Day != d {
			groups = append(groups, dayGroup{Day: d})
		}
		groups[len(groups)-1].Events = append(groups[len(groups)-1].Events, e)
	}
	s.render(w, r, 200, "timeline", page{Title: "Timeline", Nav: "timeline", Data: map[string]any{
		"Day": day.Format(dayLayout), "Prev": day.AddDate(0, 0, -1).Format(dayLayout), "Next": day.AddDate(0, 0, 1).Format(dayLayout),
		"Project": project, "Groups": groups,
	}})
}

// ---- 5. Event detail, correct, forget ----

func (s *server) event(w http.ResponseWriter, r *http.Request) {
	e, err := s.store.GetEvent(r.Context(), s.roots, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, 200, "event", page{Title: e.Title, Nav: "timeline", Data: map[string]any{"E": e}})
}

type noteFields struct{ Type, Project, Scope, Title, Body, NextActions, Key string }

func (s *server) correctForm(w http.ResponseWriter, r *http.Request) {
	e, err := s.store.GetEvent(r.Context(), s.roots, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, 200, "correct", page{Title: "Correct event", Nav: "timeline", Data: map[string]any{
		"ID": e.ID, "Key": randomKey(), "Form": noteFields{Title: e.Title, Body: e.Body, NextActions: joinLines(e.NextActions)},
	}})
}

func (s *server) correctSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.parseForm(w, r) {
		return
	}
	orig, err := s.store.GetEvent(r.Context(), s.roots, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f := noteFields{Title: r.PostFormValue("title"), Body: r.PostFormValue("body"), NextActions: r.PostFormValue("next_actions"), Key: r.PostFormValue("idempotency_key")}
	ev, _, err := s.createNote(r.Context(), noteInput{
		Type: "correction", internal: true, Project: orig.Project, Scope: orig.Scope, Title: f.Title, Body: f.Body,
		NextActions: splitLines(f.NextActions), Key: f.Key, Supersedes: id,
	})
	var bad inputError
	if errors.As(err, &bad) {
		s.render(w, r, http.StatusBadRequest, "correct", page{Title: "Correct event", Nav: "timeline", Err: string(bad),
			Data: map[string]any{"ID": id, "Key": f.Key, "Form": f}})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/e/"+url.PathEscape(ev.ID)+"?done=saved")
}

func (s *server) forgetPage(w http.ResponseWriter, r *http.Request, code int, msg, what, detail, action, back string) {
	s.render(w, r, code, "forget", page{Title: "Forget " + what, Nav: "timeline", Err: msg,
		Data: map[string]any{"What": what, "Detail": detail, "Action": action, "Back": back}})
}

func (s *server) forgetEventForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, err := s.store.GetEvent(r.Context(), s.roots, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.forgetPage(w, r, 200, "", "event", e.Title, "/e/"+url.PathEscape(id)+"/forget", "/e/"+url.PathEscape(id))
}

func (s *server) forgetEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.parseForm(w, r) {
		return
	}
	if r.PostFormValue("confirm") != "yes" {
		s.forgetPage(w, r, http.StatusBadRequest, "Confirm to forget this event.", "event", id, "/e/"+url.PathEscape(id)+"/forget", "/e/"+url.PathEscape(id))
		return
	}
	if err := s.store.ForgetEvent(r.Context(), s.roots, id, memoryapi.ActorUser); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/timeline?done=forgotten")
}

// ---- 6. New note ----

type noteInput struct {
	Type, Project, Scope, Title, Body, Key, Supersedes string
	NextActions                                        []string
	OccurredAt                                         time.Time
	internal                                           bool // type chosen by the editor, not the form
}

func (s *server) createNote(ctx context.Context, in noteInput) (memoryapi.Event, bool, error) {
	if in.Type == "" {
		in.Type = "manual_note"
	}
	if in.Scope == "" {
		in.Scope = s.cfg.DefaultScope
	}
	switch {
	case !in.internal && !validNoteType(in.Type):
		return memoryapi.Event{}, false, inputError("Pick one of the offered note types.")
	case !s.scopeAllowed(in.Scope):
		return memoryapi.Event{}, false, inputError("That scope is not one this editor can write to.")
	case !validTitle(in.Title):
		return memoryapi.Event{}, false, inputError("Add a title of 1 to 200 characters.")
	case !validProject(in.Project):
		return memoryapi.Event{}, false, inputError("The project name is too long.")
	case !validKey(in.Key):
		return memoryapi.Event{}, false, inputError("The idempotency key is not valid.")
	}
	when := in.OccurredAt
	if when.IsZero() {
		when = time.Now().UTC()
	}
	return s.store.AppendEvent(ctx, s.roots, memoryapi.Event{
		IdempotencyKey: in.Key, Type: in.Type, Title: strings.TrimSpace(in.Title), Body: in.Body,
		Project: strings.TrimSpace(in.Project), Scope: in.Scope, Client: clientName, Actor: memoryapi.ActorUser,
		OccurredAt: when, NextActions: in.NextActions, Supersedes: in.Supersedes,
	})
}

func (s *server) newPage(w http.ResponseWriter, r *http.Request, code int, f noteFields, msg string) {
	var names []string
	if ps, err := s.store.Projects(r.Context(), s.roots); err == nil {
		for _, p := range ps {
			names = append(names, p.Project)
		}
	}
	s.render(w, r, code, "new", page{Title: "New note", Nav: "new", Err: msg, Data: map[string]any{
		"Form": f, "Types": noteTypes, "Roots": s.roots, "ProjectNames": names,
	}})
}

func (s *server) newForm(w http.ResponseWriter, r *http.Request) {
	s.newPage(w, r, 200, noteFields{Type: "manual_note", Scope: s.cfg.DefaultScope, Key: randomKey()}, "")
}

func isJSON(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "application/json"
}

func (s *server) newSave(w http.ResponseWriter, r *http.Request) {
	if isJSON(r) {
		s.newJSON(w, r)
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	f := noteFields{
		Type: r.PostFormValue("type"), Project: r.PostFormValue("project"), Scope: r.PostFormValue("scope"),
		Title: r.PostFormValue("title"), Body: r.PostFormValue("body"), NextActions: r.PostFormValue("next_actions"),
		Key: r.PostFormValue("idempotency_key"),
	}
	ev, _, err := s.createNote(r.Context(), noteInput{Type: f.Type, Project: f.Project, Scope: f.Scope, Title: f.Title, Body: f.Body, Key: f.Key, NextActions: splitLines(f.NextActions)})
	var bad inputError
	if errors.As(err, &bad) {
		s.newPage(w, r, http.StatusBadRequest, f, string(bad))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/e/"+url.PathEscape(ev.ID)+"?done=created")
}

func jsonReply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) newJSON(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type           string   `json:"type"`
		Project        string   `json:"project"`
		Scope          string   `json:"scope"`
		Title          string   `json:"title"`
		Body           string   `json:"body"`
		NextActions    []string `json:"next_actions"`
		IdempotencyKey string   `json:"idempotency_key"`
		OccurredAt     string   `json:"occurred_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		if bodyTooLarge(err) {
			jsonReply(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Too large (64 KiB limit)."})
		} else {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "The note could not be read."})
		}
		return
	}
	var when time.Time
	if in.OccurredAt != "" {
		t, err := time.Parse(time.RFC3339, in.OccurredAt)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "occurred_at must be an RFC 3339 time."})
			return
		}
		when = t.UTC()
	}
	var next []string
	for _, a := range in.NextActions {
		if a = strings.TrimSpace(a); a != "" {
			next = append(next, a)
		}
	}
	ev, created, err := s.createNote(r.Context(), noteInput{Type: in.Type, Project: in.Project, Scope: in.Scope, Title: in.Title, Body: in.Body, Key: in.IdempotencyKey, NextActions: next, OccurredAt: when})
	var bad inputError
	switch {
	case errors.As(err, &bad):
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": string(bad)})
	case errors.Is(err, memoryapi.ErrInvalid):
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "The memory service did not accept this note."})
	case errors.Is(err, memoryapi.ErrScope):
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "That scope is not permitted."})
	case err != nil:
		jsonReply(w, http.StatusBadGateway, map[string]string{"error": "The memory service is not responding."})
	default:
		jsonReply(w, http.StatusOK, map[string]any{"id": ev.ID, "created": created})
	}
}

// ---- 7. Memories ----

type memoryView struct {
	M       memoryapi.Memory
	Actions []memoryAction
}

func (s *server) memories(w http.ResponseWriter, r *http.Request) {
	st := r.URL.Query().Get("status")
	if st == "" {
		st = "accepted"
	}
	statuses := []string{st}
	if st == "all" {
		statuses = memoryStatuses()
	} else if !contains(memoryStatuses(), st) {
		s.problem(w, r, http.StatusBadRequest, "That status filter is not valid.")
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	ms, err := s.store.SearchMemories(r.Context(), s.roots, memoryapi.MemoryQuery{Project: project, Statuses: statuses, Limit: 100})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]memoryView, len(ms))
	for i, m := range ms {
		items[i] = memoryView{M: m, Actions: actionsFor(m.Status)}
	}
	s.render(w, r, 200, "memories", page{Title: "Memories", Nav: "", Data: map[string]any{
		"Status": st, "Project": project, "Items": items, "Statuses": append([]string{"all"}, memoryStatuses()...),
	}})
}

func (s *server) memoryPage(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMemory(r.Context(), s.roots, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, 200, "memory", page{Title: "Memory", Nav: "", Data: map[string]any{"Item": memoryView{M: m, Actions: actionsFor(m.Status)}}})
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func (s *server) transition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.parseForm(w, r) {
		return
	}
	m, err := s.store.GetMemory(r.Context(), s.roots, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	act, ok := findAction(r.PostFormValue("to"))
	reason, statement := r.PostFormValue("reason"), r.PostFormValue("statement")
	switch {
	case !ok:
		s.problem(w, r, http.StatusBadRequest, "That is not a change this editor can make.")
		return
	case !policy.ValidMemoryTransition(m.Status, act.To):
		s.problem(w, r, http.StatusBadRequest, "A "+m.Status+" memory cannot become "+act.To+".")
		return
	case !nonBlank(reason):
		s.problem(w, r, http.StatusBadRequest, "Give a reason for the change.")
		return
	case act.NeedsStatement && !nonBlank(statement):
		s.problem(w, r, http.StatusBadRequest, "Write the new statement that replaces this one.")
		return
	}
	if !act.NeedsStatement {
		statement = ""
	}
	if _, err := s.store.CorrectMemory(r.Context(), s.roots, userCorrection(id, act.To, strings.TrimSpace(reason), strings.TrimSpace(statement))); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/memories?status=all&done=updated")
}

func (s *server) forgetMemoryForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.store.GetMemory(r.Context(), s.roots, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.forgetPage(w, r, 200, "", "memory", m.Statement, "/memories/"+url.PathEscape(id)+"/forget", "/memories?status=all")
}

func (s *server) forgetMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.parseForm(w, r) {
		return
	}
	if r.PostFormValue("confirm") != "yes" {
		s.forgetPage(w, r, http.StatusBadRequest, "Confirm to forget this memory.", "memory", id, "/memories/"+url.PathEscape(id)+"/forget", "/memories?status=all")
		return
	}
	if err := s.store.ForgetMemory(r.Context(), s.roots, id, memoryapi.ActorUser); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/memories?status=all&done=forgotten")
}

// ---- 8. Review ----

func (s *server) review(w http.ResponseWriter, r *http.Request) {
	ms, err := s.store.SearchMemories(r.Context(), s.roots, memoryapi.MemoryQuery{Statuses: policy.MemoryStatuses(policy.IsUnconfirmedMemory), Limit: 100})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, 200, "review", page{Title: "Review", Nav: "review", Data: map[string]any{"Items": ms}})
}

func (s *server) reviewDecide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.parseForm(w, r) {
		return
	}
	m, err := s.store.GetMemory(r.Context(), s.roots, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	action := r.PostFormValue("action")
	target := map[string]string{"accept": "accepted", "reject": "rejected", "edit": "accepted"}[action]
	if target == "" {
		s.problem(w, r, http.StatusBadRequest, "Choose Accept, Reject, or Edit & accept.")
		return
	}
	if !policy.ValidMemoryTransition(m.Status, target) {
		s.problem(w, r, http.StatusBadRequest, "This memory is already "+m.Status+", so it can no longer be reviewed.")
		return
	}
	switch action {
	case "accept", "reject":
		if reason == "" {
			reason = map[string]string{"accept": "accepted in review", "reject": "rejected in review"}[action]
		}
		_, err = s.store.CorrectMemory(r.Context(), s.roots, userCorrection(id, target, reason, ""))
	case "edit":
		statement := strings.TrimSpace(r.PostFormValue("statement"))
		if statement == "" {
			s.problem(w, r, http.StatusBadRequest, "The edited statement cannot be empty.")
			return
		}
		if _, err = s.store.ProposeMemory(r.Context(), s.roots, memoryapi.Memory{
			Category: m.Category, Statement: statement, Scope: m.Scope, Project: m.Project, Evidence: m.Evidence,
			Probability: -1, // unscored: the owner wrote this wording
		}, true); err == nil {
			_, err = s.store.CorrectMemory(r.Context(), s.roots, userCorrection(id, "rejected", "replaced by edited version", ""))
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/review?done=reviewed")
}
