package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type source interface {
	myIssues(ctx context.Context) ([]issue, error)
	// createdIssues is the issues you created lately, newest first, in any
	// state and anyone's: what you just filed, wherever it landed.
	createdIssues(ctx context.Context) ([]issue, error)
	projects(ctx context.Context) ([]project, error)
	projectIssues(ctx context.Context, projectID string) ([]issue, error)
	issueDetail(ctx context.Context, id string) (*issueDetail, error)
	projectDetail(ctx context.Context, id string) (*projectDetail, error)
	teamStates(ctx context.Context, teamID string) ([]workflowState, error)
	setState(ctx context.Context, issueID, stateID string) error
	// freshIssue re-reads one issue, for decisions that must not rest on what
	// the popup loaded minutes ago.
	freshIssue(ctx context.Context, id string) (issue, error)
	// startIssue applies start to an issue as freshIssue returned it.
	startIssue(ctx context.Context, is issue) error
	// issuesByNumber finds every team's issue with that number, open or
	// closed, anyone's; only teamKey's when it isn't "".
	issuesByNumber(ctx context.Context, number int, teamKey string) ([]issue, error)
	// teams lists the workspace's teams, for a new issue to go in.
	teams(ctx context.Context) ([]teamInfo, error)
	createIssue(ctx context.Context, n newIssue) (issue, error)
	// uploadFile puts a file in Jira's storage, for a description to
	// show: its URL (attach.go).
	uploadFile(ctx context.Context, path string) (string, error)
}

type workflowState struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"` // triage | backlog | unstarted | started | completed | canceled
	Color    string  `json:"color"`
	Position float64 `json:"position"`
}

type team struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type projectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type person struct {
	ID   string `json:"id"`
	Name string `json:"displayName"`
	IsMe bool   `json:"isMe"`
}

type issue struct {
	Warning    string        `json:"-"`
	ID         string        `json:"id"`
	Identifier string        `json:"identifier"`
	Title      string        `json:"title"`
	BranchName string        `json:"branchName"`
	URL        string        `json:"url"`
	Priority   int           `json:"priority"`
	State      workflowState `json:"state"`
	Team       team          `json:"team"`
	Project    *projectRef   `json:"project"`
	Assignee   *person       `json:"assignee"`
}

type projectStatus struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // backlog | planned | started | paused | completed | canceled
	Color string `json:"color"`
}

type teamKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type project struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	SlugID     string        `json:"slugId"`
	URL        string        `json:"url"`
	Color      string        `json:"color"`
	Progress   float64       `json:"progress"`
	TargetDate string        `json:"targetDate"`
	Status     projectStatus `json:"status"`
	Lead       *person       `json:"lead"`
	Teams      struct {
		Nodes []teamKey `json:"nodes"`
	} `json:"teams"`
}

type label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type cycle struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

type issueDetail struct {
	Description   string `json:"description"`
	PriorityLabel string `json:"priorityLabel"`
	DueDate       string `json:"dueDate"`
	Labels        struct {
		Nodes []label `json:"nodes"`
	} `json:"labels"`
	Cycle *cycle `json:"cycle"`
}

type projectDetail struct {
	Description string  `json:"description"`
	Content     string  `json:"content"`
	StartDate   string  `json:"startDate"`
	Lead        *person `json:"lead"`
}

func stateRank(t string) int {
	switch t {
	case "started":
		return 0
	case "unstarted":
		return 1
	case "triage":
		return 2
	case "backlog":
		return 3
	}
	return 4
}

// The order a status picker lists states in: the way work flows.
func lifecycleRank(t string) int {
	switch t {
	case "triage":
		return 0
	case "backlog":
		return 1
	case "unstarted":
		return 2
	case "started":
		return 3
	case "completed":
		return 4
	case "canceled":
		return 5
	}
	return 6
}

func projectRank(t string) int {
	switch t {
	case "started":
		return 0
	case "planned":
		return 1
	case "paused":
		return 2
	case "backlog":
		return 3
	}
	return 4
}

// Jira's priority: 1 urgent … 4 low, 0 none (sorts last).
func priorityRank(p int) int {
	if p == 0 {
		return 5
	}
	return p
}

func sortIssues(is []issue) {
	// Teams number their states independently, so "In Progress" can sit at a
	// different position in each. Order by state name, placing each name at
	// its lowest position, so one status stays one group across teams.
	namePos := map[string]float64{}
	for _, x := range is {
		if p, ok := namePos[x.State.Name]; !ok || x.State.Position < p {
			namePos[x.State.Name] = x.State.Position
		}
	}
	sort.SliceStable(is, func(i, j int) bool {
		a, b := is[i], is[j]
		if ra, rb := stateRank(a.State.Type), stateRank(b.State.Type); ra != rb {
			return ra < rb
		}
		if a.State.Name != b.State.Name {
			pa, pb := namePos[a.State.Name], namePos[b.State.Name]
			if pa != pb {
				// Within "started", later is further along (In Review after
				// In Progress): show those first, they're closest to done.
				if a.State.Type == "started" {
					return pa > pb
				}
				return pa < pb
			}
			return a.State.Name < b.State.Name
		}
		return priorityRank(a.Priority) < priorityRank(b.Priority)
	})
}

func (is issue) matches(filter string) bool {
	if filter == "" {
		return true
	}
	hay := strings.ToLower(is.Identifier + " " + is.Title + " " + is.State.Name)
	if is.Project != nil {
		hay += " " + strings.ToLower(is.Project.Name)
	}
	for _, word := range strings.Fields(strings.ToLower(filter)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}

func (p project) matches(filter string) bool {
	hay := strings.ToLower(p.Name + " " + p.Status.Name)
	for _, word := range strings.Fields(strings.ToLower(filter)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}

func startConflict(is issue) error {
	if is.State.Type == "completed" || is.State.Type == "canceled" {
		return fmt.Errorf("%s is %s; it cannot be started", is.Identifier, is.State.Name)
	}
	if is.Assignee != nil && !is.Assignee.IsMe {
		return fmt.Errorf("%s is assigned to %s; Start is available for your own or unassigned issues", is.Identifier, is.Assignee.Name)
	}
	return nil
}
