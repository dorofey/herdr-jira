package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const keychainService = "herdr-jira"

var readStore = loadTokens
var writeStore = saveTokens
var removeStore = deleteTokens
var httpClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
var errSignedOut = errors.New("not signed in to Jira; sign in with your site, email and API token")
var errNotSignedIn = errors.New("not signed in")

type tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type credentials struct {
	Site  string `json:"site"`
	Email string `json:"email"`
	Token string `json:"token"`
}
type loginUI struct {
	status func(string)
	link   func(string, bool)
	pasted <-chan string
}

const pasteHint = "Enter your Jira HTTPS site, then email, then API token. Input is hidden. Tokens are stored in your system secret store."

func (ui loginUI) say(s string) {
	if ui.status != nil {
		ui.status(s)
	}
}
func login(ctx context.Context, cfg config, ui loginUI) (workspace, error) {
	vals := []string{os.Getenv("JIRA_URL"), os.Getenv("JIRA_EMAIL"), os.Getenv("JIRA_API_TOKEN")}
	prompts := []string{"Jira site URL (https://your-team.atlassian.net)", "Atlassian email", "Atlassian API token"}
	for i := range vals {
		if vals[i] == "" {
			ui.say(prompts[i])
			select {
			case <-ctx.Done():
				return workspace{}, ctx.Err()
			case v, ok := <-ui.pasted:
				if !ok {
					return workspace{}, errors.New("sign-in input ended")
				}
				vals[i] = strings.TrimSpace(v)
			}
		}
		if vals[i] == "" {
			return workspace{}, errors.New("sign-in fields cannot be empty")
		}
	}
	u, err := url.Parse(strings.TrimRight(vals[0], "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return workspace{}, errors.New("use your Jira site's HTTPS URL without a path, query or credentials")
	}
	cr := credentials{Site: u.String(), Email: vals[1], Token: vals[2]}
	raw, _ := json.Marshal(cr)
	w := workspace{ID: u.Host, Name: u.Host, URLKey: u.Host}
	c := &jiraClient{cfg: cfg, ws: w, credential: &cr}
	if _, err := c.viewerID(ctx); err != nil {
		return workspace{}, err
	}
	unlock, err := lockTokens(ctx)
	if err != nil {
		return workspace{}, err
	}
	defer unlock()
	if err := writeStore(account(w.ID), &tokens{AccessToken: string(raw)}); err != nil {
		return workspace{}, err
	}
	if err := updateIndexLocked(func(ix *workspaceIndex) { ix.add(w) }); err != nil {
		return workspace{}, err
	}
	return w, nil
}
func logout(ctx context.Context, cfg config, which string, local bool) ([]string, error) {
	unlock, err := lockTokens(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ix, err := readIndexLocked()
	if err != nil {
		return nil, err
	}
	targets := ix.Workspaces
	if which != "" {
		w, err := ix.find(which)
		if err != nil {
			return nil, err
		}
		targets = []workspace{*w}
	}
	var done []string
	for _, w := range targets {
		if err := removeStore(account(w.ID)); err != nil && !errors.Is(err, errSignedOut) {
			return done, err
		}
		dropListCache(w.ID)
		if err := updateIndexLocked(func(ix *workspaceIndex) { ix.remove(w.ID) }); err != nil {
			return done, err
		}
		done = append(done, w.Name)
	}
	if len(done) == 0 {
		return nil, errNotSignedIn
	}
	return done, nil
}

var lockWait = 15 * time.Second

// lockTokens takes the lock that every change to the stored tokens goes
// through (refresh, sign-in, sign-out), shared by all herdr-jira processes,
// and returns its release. It gives up after lockWait, or when ctx ends,
// rather than waiting forever on a stuck process; and if the lock can't be
// taken at all, that's an error, not a silent unlocked change.
func lockTokens(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return nil, fmt.Errorf("token lock: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(stateDir(), "token.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("token lock: %w", err)
	}
	deadline := time.Now().Add(lockWait)
	for {
		// Checked first: a cancelled command must not go on to change the
		// tokens just because the lock happened to be free.
		if ctx.Err() != nil {
			f.Close()
			return nil, ctx.Err()
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("token lock: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errors.New("another herdr-jira is busy updating your Jira sign-in; try again in a moment")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func overSSH() bool { return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" }

var noBrowser = remoteSession
