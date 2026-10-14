package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const maxItems = 1000
const recentCreated = 50

var errTruncated = errors.New("showing the first 1000 items; open Jira to see the rest")
var errIncomplete = errors.New("Jira pagination did not advance; refresh to retry")

type jiraClient struct {
	credsMu    sync.Mutex
	cfg        config
	ws         workspace
	credential *credentials
}

func (c *jiraClient) creds() (credentials, error) {
	c.credsMu.Lock()
	defer c.credsMu.Unlock()
	if c.credential != nil {
		return *c.credential, nil
	}
	t, err := readStore(account(c.ws.ID))
	if err != nil {
		return credentials{}, err
	}
	var cr credentials
	err = json.Unmarshal([]byte(t.AccessToken), &cr)
	if err != nil || cr.Site == "" || cr.Email == "" || cr.Token == "" {
		return cr, errSignedOut
	}
	c.credential = &cr
	return cr, nil
}
func (c *jiraClient) request(ctx context.Context, method, path string, input, out any) error {
	cr, err := c.creds()
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, cr.Site+"/rest/api/3/"+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cr.Email, cr.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 401 {
		return errSignedOut
	}
	if res.StatusCode == 429 {
		return fmt.Errorf("Jira rate limit reached; retry after %s seconds", res.Header.Get("Retry-After"))
	}
	if res.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		var msg struct {
			ErrorMessages []string          `json:"errorMessages"`
			Errors        map[string]string `json:"errors"`
		}
		json.Unmarshal(raw, &msg)
		parts := msg.ErrorMessages
		for k, v := range msg.Errors {
			parts = append(parts, k+": "+v)
		}
		sort.Strings(parts)
		return fmt.Errorf("Jira HTTP %d: %s", res.StatusCode, clean(strings.Join(parts, "; "), false))
	}
	if out == nil || res.StatusCode == 204 {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("Jira response: %w", err)
	}
	sanitize(out)
	return nil
}

type jiraStatus struct {
	ID, Name       string
	StatusCategory struct{ Key string } `json:"statusCategory"`
}

func state(s jiraStatus) workflowState {
	t, col := "unstarted", "#8c9bab"
	switch s.StatusCategory.Key {
	case "indeterminate":
		t, col = "started", "#579dff"
	case "done":
		t, col = "completed", "#4bce97"
	}
	return workflowState{ID: s.ID, Name: s.Name, Type: t, Color: col}
}

type jiraProject struct {
	ID, Key, Name, Description string
	Lead                       *struct{ AccountID, DisplayName string }
}
type jiraIssue struct {
	ID, Key string
	Fields  struct {
		Summary     string
		Description json.RawMessage
		Status      jiraStatus
		Project     jiraProject
		Assignee    *struct{ AccountID, DisplayName string }
		Priority    *struct{ ID, Name string }
		Labels      []string
		DueDate     string `json:"duedate"`
	}
}

func (c *jiraClient) convert(j jiraIssue, me string) issue {
	is := issue{ID: j.Key, Identifier: j.Key, Title: j.Fields.Summary, BranchName: strings.ToLower(j.Key), URL: c.site() + "/browse/" + j.Key, State: state(j.Fields.Status), Team: team{ID: j.Fields.Project.ID, Key: j.Fields.Project.Key}, Project: &projectRef{ID: j.Fields.Project.ID, Name: j.Fields.Project.Name}}
	if a := j.Fields.Assignee; a != nil {
		is.Assignee = &person{ID: a.AccountID, Name: a.DisplayName, IsMe: a.AccountID == me}
	}
	if p := j.Fields.Priority; p != nil {
		is.Priority = priorityLevel(p.Name)
	}
	sanitize(&is)
	return is
}
func slug(s string) string {
	var b strings.Builder
	sep := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if sep && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			sep = false
		} else {
			sep = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	if b.Len() == 0 {
		return "issue"
	}
	return b.String()
}
func (c *jiraClient) site() string {
	cr, err := c.creds()
	if err != nil {
		return ""
	}
	return cr.Site
}
func (c *jiraClient) viewerID(ctx context.Context) (string, error) {
	var v struct{ AccountID string }
	err := c.request(ctx, "GET", "myself", nil, &v)
	if err == nil && v.AccountID == "" {
		err = errors.New("Jira did not identify the current user")
	}
	return v.AccountID, err
}
func (c *jiraClient) search(ctx context.Context, jql string) ([]issue, error) {
	me, err := c.viewerID(ctx)
	if err != nil {
		return nil, err
	}
	var all []issue
	next := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Issues        []jiraIssue
			NextPageToken string
			IsLast        bool
		}
		err := c.request(ctx, "POST", "search/jql", map[string]any{"jql": jql, "maxResults": 100, "nextPageToken": next, "fields": []string{"summary", "status", "project", "assignee", "priority"}}, &page)
		if err != nil {
			return all, err
		}
		for _, j := range page.Issues {
			all = append(all, c.convert(j, me))
		}
		if page.IsLast || page.NextPageToken == "" {
			if !strings.Contains(jql, "ORDER BY created") {
				sortIssues(all)
			}
			return all, nil
		}
		if len(all) >= maxItems {
			sortIssues(all)
			return all[:maxItems], errTruncated
		}
		if len(page.Issues) == 0 {
			return all, errIncomplete
		}
		if seen[page.NextPageToken] {
			return all, errIncomplete
		}
		seen[page.NextPageToken] = true
		next = page.NextPageToken
	}
}
func (c *jiraClient) myIssues(ctx context.Context) ([]issue, error) {
	return c.search(ctx, "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC")
}
func (c *jiraClient) createdIssues(ctx context.Context) ([]issue, error) {
	return c.search(ctx, "creator = currentUser() ORDER BY created DESC")
}
func (c *jiraClient) projectIssues(ctx context.Context, id string) ([]issue, error) {
	return c.search(ctx, "project = "+jqlQuote(id)+" AND statusCategory != Done ORDER BY updated DESC")
}
func jqlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
func (c *jiraClient) freshIssue(ctx context.Context, id string) (issue, error) {
	me, err := c.viewerID(ctx)
	if err != nil {
		return issue{}, err
	}
	var j jiraIssue
	err = c.request(ctx, "GET", "issue/"+url.PathEscape(id), nil, &j)
	return c.convert(j, me), err
}
func (c *jiraClient) issuesByNumber(ctx context.Context, n int, key string) ([]issue, error) {
	if key == "" {
		return nil, errors.New("enter the full Jira issue key, such as APP-123")
	}
	return c.search(ctx, "key = "+jqlQuote(strings.ToUpper(key)+"-"+strconv.Itoa(n)))
}
func (c *jiraClient) projects(ctx context.Context) ([]project, error) {
	var all []project
	start := 0
	for {
		var p struct {
			Values []jiraProject
			IsLast bool
			Total  int
		}
		if err := c.request(ctx, "GET", fmt.Sprintf("project/search?startAt=%d&maxResults=100", start), nil, &p); err != nil {
			return all, err
		}
		for _, j := range p.Values {
			v := project{ID: j.ID, SlugID: j.Key, Name: j.Name, URL: c.site() + "/plugins/servlet/project-config/" + j.Key, Color: "#579dff", Progress: -1, Status: projectStatus{Name: "Active", Type: "started"}}
			v.Teams.Nodes = []teamKey{{ID: j.ID, Key: j.Key}}
			all = append(all, v)
		}
		start += len(p.Values)
		if p.IsLast || start >= p.Total {
			sortProjects(all)
			return all, nil
		}
		if len(p.Values) == 0 {
			return all, errIncomplete
		}
		if len(all) >= maxItems {
			return all[:maxItems], errTruncated
		}
	}
}
func sortProjects(ps []project) {
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
}
func (c *jiraClient) projectDetail(ctx context.Context, id string) (*projectDetail, error) {
	var j jiraProject
	if err := c.request(ctx, "GET", "project/"+url.PathEscape(id), nil, &j); err != nil {
		return nil, err
	}
	d := &projectDetail{Description: j.Description}
	if j.Lead != nil {
		d.Lead = &person{ID: j.Lead.AccountID, Name: j.Lead.DisplayName}
	}
	return d, nil
}
func (c *jiraClient) issueDetail(ctx context.Context, id string) (*issueDetail, error) {
	var j jiraIssue
	if err := c.request(ctx, "GET", "issue/"+url.PathEscape(id), nil, &j); err != nil {
		return nil, err
	}
	d := &issueDetail{Description: adfMarkdown(j.Fields.Description), DueDate: j.Fields.DueDate}
	if j.Fields.Priority != nil {
		d.PriorityLabel = j.Fields.Priority.Name
	}
	for _, l := range j.Fields.Labels {
		d.Labels.Nodes = append(d.Labels.Nodes, label{Name: l, Color: "#8c9bab"})
	}
	sanitize(d)
	return d, nil
}

type jiraTransition struct {
	ID, Name  string
	To        jiraStatus
	HasScreen bool
	Fields    map[string]struct{ Required bool }
}

func (c *jiraClient) transitions(ctx context.Context, id string) ([]jiraTransition, error) {
	var v struct{ Transitions []jiraTransition }
	err := c.request(ctx, "GET", "issue/"+url.PathEscape(id)+"/transitions?expand=transitions.fields", nil, &v)
	return v.Transitions, err
}
func (c *jiraClient) teamStates(ctx context.Context, id string) ([]workflowState, error) {
	if !strings.Contains(id, "-") {
		return nil, nil
	} // Creation uses Jira's initial workflow status.
	ts, err := c.transitions(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []workflowState
	for _, t := range ts {
		s := state(t.To)
		s.ID = t.ID
		s.Name = t.Name
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errors.New("no workflow transitions are available for this issue")
	}
	return out, nil
}
func (c *jiraClient) setState(ctx context.Context, id, transition string) error {
	return c.request(ctx, "POST", "issue/"+url.PathEscape(id)+"/transitions", map[string]any{"transition": map[string]string{"id": transition}}, nil)
}
func (c *jiraClient) startIssue(ctx context.Context, is issue) error {
	fresh, err := c.freshIssue(ctx, is.ID)
	if err != nil {
		return err
	}
	if err := startConflict(fresh); err != nil {
		return err
	}
	me, err := c.viewerID(ctx)
	if err != nil {
		return err
	}
	var chosen *jiraTransition
	if fresh.State.Type != "started" {
		ts, err := c.transitions(ctx, is.ID)
		if err != nil {
			return err
		}
		for _, t := range ts {
			if state(t.To).Type == "started" {
				v := t
				if chosen != nil {
					return errors.New("multiple In Progress transitions; choose a status first")
				}
				chosen = &v
			}
		}
		if chosen == nil {
			return errors.New("no available transition to In Progress")
		}
		for k, f := range chosen.Fields {
			if f.Required && k != "assignee" {
				return fmt.Errorf("transition needs %s; use Jira to supply required fields", k)
			}
		}
	}
	if fresh.Assignee == nil {
		if err := c.request(ctx, "PUT", "issue/"+url.PathEscape(is.ID)+"/assignee", map[string]string{"accountId": me}, nil); err != nil {
			return err
		}
	}
	if chosen != nil {
		if err := c.setState(ctx, is.ID, chosen.ID); err != nil {
			return fmt.Errorf("assignment succeeded but status transition failed: %w", err)
		}
	}
	return nil
}
func (c *jiraClient) teams(ctx context.Context) ([]teamInfo, error) {
	ps, err := c.projects(ctx)
	var ts []teamInfo
	for _, p := range ps {
		ts = append(ts, teamInfo{ID: p.ID, Key: p.SlugID, Name: p.Name})
	}
	return ts, err
}
func (c *jiraClient) createIssue(ctx context.Context, n newIssue) (issue, error) {
	var meta struct {
		IssueTypes []struct {
			ID, Name string
			Subtask  bool
		}
	}
	if err := c.request(ctx, "GET", "issue/createmeta/"+url.PathEscape(n.TeamID)+"/issuetypes", nil, &meta); err != nil {
		return issue{}, err
	}
	kind := ""
	for _, t := range meta.IssueTypes {
		if !t.Subtask && (kind == "" || strings.EqualFold(t.Name, "Task")) {
			kind = t.ID
		}
	}
	if kind == "" {
		return issue{}, errors.New("project has no available standard issue type")
	}
	if n.ProjectID != "" && n.ProjectID != n.TeamID {
		return issue{}, errors.New("choose the same Jira project in both project fields")
	}
	f := map[string]any{"project": map[string]string{"id": n.TeamID}, "issuetype": map[string]string{"id": kind}, "summary": n.Title, "description": plainADF(n.Description)}
	if n.AssignToMe {
		me, err := c.viewerID(ctx)
		if err != nil {
			return issue{}, err
		}
		f["assignee"] = map[string]string{"accountId": me}
	}
	if n.Priority > 0 {
		var priorities []struct{ ID, Name string }
		if err := c.request(ctx, "GET", "priority", nil, &priorities); err != nil {
			return issue{}, err
		}
		found := ""
		for _, p := range priorities {
			if priorityLevel(p.Name) == n.Priority {
				found = p.ID
				break
			}
		}
		if found == "" {
			return issue{}, errors.New("selected priority has no matching Jira priority; choose None for project default")
		}
		f["priority"] = map[string]string{"id": found}
	}

	var v struct{ Key string }
	if err := c.request(ctx, "POST", "issue", map[string]any{"fields": f}, &v); err != nil {
		return issue{}, err
	}
	is, err := c.freshIssue(ctx, v.Key)
	if err != nil {
		is = issue{ID: v.Key, Identifier: v.Key, Title: n.Title, URL: c.site() + "/browse/" + v.Key, BranchName: strings.ToLower(v.Key), Team: team{ID: n.TeamID}, State: workflowState{Type: "unstarted"}}
		is.Warning = "Created " + v.Key + "; reload failed. Open Jira to check details."
	}
	for _, path := range n.Files {
		if err := c.attachFile(ctx, v.Key, path); err != nil {
			is.Warning = "Created " + v.Key + "; attachment failed: " + err.Error() + ". Add remaining files in Jira."
			break
		}
	}

	return is, nil
}
func (c *jiraClient) uploadFile(ctx context.Context, path string) (string, error) {
	return "", errors.New("Jira attachments require an existing issue; create it and attach files in Jira")
}
func priorityLevel(name string) int {
	switch strings.ToLower(name) {
	case "highest", "urgent", "blocker":
		return 1
	case "high", "critical":
		return 2
	case "medium", "normal", "major":
		return 3
	case "low", "lowest", "minor", "trivial":
		return 4
	}
	return 0
}
