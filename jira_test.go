package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type handlerTransport struct{ handler http.HandlerFunc }

func (h handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	h.handler(w, r)
	return w.Result(), nil
}
func mockJira(t *testing.T, h http.HandlerFunc) *jiraClient {
	t.Helper()
	old := httpClient
	httpClient = &http.Client{Transport: handlerTransport{h}}
	t.Cleanup(func() { httpClient = old })
	return &jiraClient{credential: &credentials{Site: "https://demo.atlassian.net", Email: "demo@example.test", Token: "fictional"}}
}
func issueJSON(key, owner, category string) string {
	return fmt.Sprintf(`{"key":%q,"fields":{"summary":"Fix login","status":{"id":"7","name":"To Do","statusCategory":{"key":%q}},"project":{"id":"100","key":"APP","name":"App"},"assignee":{"accountId":%q,"displayName":"Owner"}}}`, key, category, owner)
}
func TestSearchPagesAndBasicAuth(t *testing.T) {
	pages := 0
	c := mockJira(t, func(w http.ResponseWriter, r *http.Request) {
		email, token, ok := r.BasicAuth()
		if !ok || email != "demo@example.test" || token != "fictional" {
			t.Error("missing basic auth")
		}
		switch r.URL.Path {
		case "/rest/api/3/myself":
			fmt.Fprint(w, `{"accountId":"me"}`)
		case "/rest/api/3/search/jql":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			pages++
			if pages == 1 {
				fmt.Fprintf(w, `{"issues":[%s],"nextPageToken":"next","isLast":false}`, issueJSON("APP-1", "me", "new"))
			} else {
				if in["nextPageToken"] != "next" {
					t.Error("wrong pagination token")
				}
				fmt.Fprintf(w, `{"issues":[%s],"isLast":true}`, issueJSON("APP-2", "me", "indeterminate"))
			}
		default:
			t.Error(r.URL.Path)
		}
	})
	is, err := c.myIssues(context.Background())
	if err != nil || len(is) != 2 || !is[0].Assignee.IsMe || is[0].Identifier != "APP-2" {
		t.Fatalf("%+v %v", is, err)
	}
}
func TestAuthenticationRejected(t *testing.T) {
	c := mockJira(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	_, err := c.myIssues(context.Background())
	if !errors.Is(err, errSignedOut) {
		t.Fatal(err)
	}
}
func TestTransitionsUseIssueKey(t *testing.T) {
	c := mockJira(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/APP-1/transitions" {
			t.Error(r.URL.Path)
		}
		if r.Method == "GET" {
			fmt.Fprint(w, `{"transitions":[{"id":"31","name":"Begin work","to":{"id":"7","name":"In Progress","statusCategory":{"key":"indeterminate"}}}]}`)
		} else {
			var b struct{ Transition struct{ ID string } }
			json.NewDecoder(r.Body).Decode(&b)
			if b.Transition.ID != "31" {
				t.Error("status id used instead of transition id")
			}
			w.WriteHeader(204)
		}
	})
	s, err := c.teamStates(context.Background(), "APP-1")
	if err != nil || len(s) != 1 || s[0].ID != "31" || s[0].Type != "started" {
		t.Fatalf("%+v %v", s, err)
	}
	if err := c.setState(context.Background(), "APP-1", s[0].ID); err != nil {
		t.Fatal(err)
	}
}
func TestStartRefusesOtherOwner(t *testing.T) {
	mutated := false
	c := mockJira(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutated = true
		}
		if strings.HasSuffix(r.URL.Path, "myself") {
			fmt.Fprint(w, `{"accountId":"me"}`)
		} else {
			fmt.Fprint(w, issueJSON("APP-1", "other", "new"))
		}
	})
	err := c.startIssue(context.Background(), issue{ID: "APP-1"})
	if err == nil || mutated {
		t.Fatal("coworker's issue was started")
	}
}
func TestAttachmentMultipart(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "note.txt")
	os.WriteFile(p, []byte("hello"), 0600)
	c := mockJira(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Atlassian-Token") != "no-check" {
			t.Error("missing attachment header")
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprint(w, `[]`)
	})
	if err := c.attachFile(context.Background(), "APP-1", p); err != nil {
		t.Fatal(err)
	}
}
func TestADFRendering(t *testing.T) {
	s := adfMarkdown(json.RawMessage(`{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Title"}]},{"type":"paragraph","content":[{"type":"text","text":"Hello","marks":[{"type":"strong"}]}]}]}`))
	if !strings.Contains(s, "## Title") || !strings.Contains(s, "**Hello**") {
		t.Fatal(s)
	}
}
func TestCloudURLs(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "https://linear.app/issue/APP-1", "https://evilatlassian.net", "https://team.atlassian.net.evil.test"} {
		if isJiraURL(u) {
			t.Fatal(u)
		}
	}
	if !isJiraURL("https://team.atlassian.net/browse/APP-1") {
		t.Fatal("cloud URL rejected")
	}
}
func TestCreatedIssueSurvivesAttachmentFailure(t *testing.T) {
	creates := 0
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	c := mockJira(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/issue/createmeta/100/issuetypes":
			fmt.Fprint(w, `{"issueTypes":[{"id":"9","name":"Task","subtask":false}]}`)
		case "/rest/api/3/issue":
			creates++
			var in struct {
				Fields struct {
					IssueType   struct{ ID string }
					Description map[string]any
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Fields.IssueType.ID != "9" || in.Fields.Description["type"] != "doc" {
				t.Error("incorrect create payload")
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"key":"APP-3"}`)
		case "/rest/api/3/myself":
			fmt.Fprint(w, `{"accountId":"me"}`)
		case "/rest/api/3/issue/APP-3":
			fmt.Fprint(w, issueJSON("APP-3", "me", "new"))
		case "/rest/api/3/issue/APP-3/attachments":
			w.WriteHeader(403)
		default:
			t.Error(r.URL.Path)
		}
	})
	is, err := c.createIssue(context.Background(), newIssue{TeamID: "100", Title: "Test", Description: "Details", Files: []string{path}})
	if err != nil || creates != 1 || is.Identifier != "APP-3" || is.Warning == "" {
		t.Fatalf("issue %+v, err %v, creates %d", is, err, creates)
	}
}
