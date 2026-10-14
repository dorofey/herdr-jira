package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Attaching files to a new issue. herdr-file-transfer chooses them: any file
// on this server, from the agent's folder on, or one it brings over from the
// machine you're at first (a drop, or Taildrop from an iPad), which this
// popup can't. ^f (or "Add new…") saves the form as a draft and hands the
// popup to herdr-file-transfer's, which hands it back with the files' paths.
// They're uploaded to Jira when the issue is created, and put in its
// description: images shown, other files linked.

const (
	fileTransferPlugin = "herdr-file-transfer"
	// draftEnv says the popup is opened on the draft saved before attaching.
	draftEnv = "HERDR_JIRA_DRAFT"
	// herdr-file-transfer's: what it's choosing files for, the folder it
	// starts in, and the files chosen, as a JSON list.
	pickEnv   = "HERDR_FILE_TRANSFER_PICK"
	pickDir   = "HERDR_FILE_TRANSFER_DIR"
	pickedEnv = "HERDR_FILE_TRANSFER_PICKED"
)

// draft is the new issue's form, kept while another popup has this one's
// place.
type draft struct {
	WorkspaceID string   `json:"workspace_id"`
	Title       string   `json:"title"`
	Desc        string   `json:"desc"`
	TeamID      string   `json:"team_id"`
	ProjectID   string   `json:"project_id"`
	StateID     string   `json:"state_id"`
	Priority    int      `json:"priority"`
	AssignMe    bool     `json:"assign_me"`
	Files       []string `json:"files"`
	Focus       int      `json:"focus"`
}

// hasPopup says whether herdr has that plugin's popup: installed, and on.
// A variable so tests can stand one in.
var hasPopup = func(p popupRef) bool {
	_, _, err := paneCommand(p)
	return err == nil
}

func draftPath() string { return filepath.Join(stateDir(), "draft.json") }

func saveDraft(d draft) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(draftPath(), data, 0o600)
}

// takeDraft reads the saved draft, once, with the files just picked added:
// those that are there, each once.
func takeDraft(picked string) (*draft, error) {
	data, err := os.ReadFile(draftPath())
	if err != nil {
		return nil, err
	}
	os.Remove(draftPath())
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	var more []string
	_ = json.Unmarshal([]byte(picked), &more)
	seen := map[string]bool{}
	var files []string
	for _, f := range append(d.Files, more...) {
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() && filepath.IsAbs(f) && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	d.Files = files
	return &d, nil
}

// attach saves the form and hands the popup to herdr-file-transfer's, to
// choose files; it hands the popup back.
func (m model) attach() (tea.Model, tea.Cmd) {
	if _, demo := m.client.(*demoSource); demo {
		m.err = "Attaching needs herdr-file-transfer, and the demo doesn't go there"
		return m, nil
	}
	if m.ws == nil {
		return m, nil
	}
	pick := popupRef{PluginID: fileTransferPlugin, Entrypoint: "picker"}
	if !hasPopup(pick) {
		m.err = "Attaching files needs the herdr-file-transfer plugin: herdr plugin install mrolafsson/herdr-file-transfer"
		return m, nil
	}
	f := m.form
	d := draft{
		WorkspaceID: m.ws.ID, Title: f.title.Value(), Desc: f.desc.Value(), TeamID: f.teamID, ProjectID: f.projectID,
		StateID: f.stateID, Priority: f.priority, AssignMe: f.assignMe, Files: f.files, Focus: f.focus,
	}
	if err := saveDraft(d); err != nil {
		m.err = "Couldn't keep the draft: " + err.Error()
		return m, nil
	}
	self := popupRef{PluginID: pluginID(), Entrypoint: "picker", Width: "80%", Height: "70%",
		Env: map[string]string{draftEnv: "1", "HERDR_JIRA_CWD": m.invoked}}
	if m.back != nil {
		data, _ := json.Marshal(m.back)
		self.Env[returnEnv] = string(data)
	}
	data, err := json.Marshal(self)
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	pick.Env = map[string]string{pickEnv: "attach", returnEnv: string(data)}
	if m.invoked != "" {
		pick.Env[pickDir] = m.invoked // the agent's folder
	}
	if m.cancel != nil {
		m.cancel()
	}
	if err := handOver(pick); err != nil {
		m.err = err.Error()
		return m, nil
	}
	return m, tea.Quit
}

// restoreDraft opens the form as it was before attaching, with the files.
func (m model) restoreDraft(d draft) (model, tea.Cmd) {
	next, cmd := m.openCreate(nil, nil)
	m = next.(model)
	f := &m.form
	f.title.SetValue(d.Title)
	f.desc.SetValue(d.Desc)
	if d.TeamID != "" {
		f.teamID = d.TeamID
	}
	f.projectID, f.stateID, f.priority, f.assignMe, f.files = d.ProjectID, d.StateID, d.Priority, d.AssignMe, d.Files
	f.fileCursor = len(f.files) // on "Add new…", for another
	if d.Focus >= 0 && d.Focus < fieldCount {
		f.setFocus(d.Focus)
	}
	return m, tea.Batch(cmd, m.loadFormStates())
}

// ── uploading ─────────────────────────────────────────────────────────────────

// contentType is a file's type, by its name, else by its first bytes.
func contentType(path string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); t != "" {
		return t
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return http.DetectContentType(head[:n])
}

// uploadFile puts a file in Jira's storage and returns its URL, for the
// description to show or link.
// attachmentMarkdown is the description's lines for uploaded files: an
// image shown, anything else a link.
func attachmentMarkdown(names, urls []string) string {
	var b strings.Builder
	for i, url := range urls {
		name := strings.NewReplacer("[", `\[`, "]", `\]`).Replace(names[i])
		if strings.HasPrefix(contentType(names[i]), "image/") {
			b.WriteString("![" + name + "](" + url + ")\n")
		} else {
			b.WriteString("[" + name + "](" + url + ")\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// withAttachments uploads the files and adds them to the description.
func withAttachments(ctx context.Context, client source, in newIssue, files []string) (newIssue, error) {
	if _, ok := client.(*jiraClient); ok {
		for _, f := range files {
			st, err := os.Stat(f)
			if err != nil {
				return in, err
			}
			if !st.Mode().IsRegular() {
				return in, fmt.Errorf("attachment is not a regular file: %s", filepath.Base(f))
			}
			if st.Size() > 25<<20 {
				return in, fmt.Errorf("attachment exceeds 25 MiB: %s", filepath.Base(f))
			}
		}
		in.Files = append([]string(nil), files...)
		return in, nil
	}
	if len(files) == 0 {
		return in, nil
	}
	var names, urls []string
	for _, f := range files {
		url, err := client.uploadFile(ctx, f)
		if err != nil {
			return in, err
		}
		names, urls = append(names, filepath.Base(f)), append(urls, url)
	}
	md := attachmentMarkdown(names, urls)
	if in.Description == "" {
		in.Description = md
	} else {
		in.Description += "\n\n" + md
	}
	return in, nil
}

// fileLabel is an attached file as the form lists it: its name and size.
func fileLabel(path string) string {
	label := filepath.Base(path)
	if st, err := os.Stat(path); err == nil {
		label += "  " + styleDim.Render(humanSize(st.Size()))
	}
	return label
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}
