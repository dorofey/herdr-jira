// herdr-jira: your Jira issues and projects in a herdr popup, one key away
// from a worktree for any of them.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"golang.org/x/term"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const usage = `herdr-jira — Jira issues and projects in herdr

  action open      what the herdr action runs: opens the picker popup
  action logout    what the herdr action runs: signs out, with a toast
  action demo      what the herdr action runs: the picker on a fictional workspace
  picker [--demo]  the popup itself; --demo (or HERDR_JIRA_DEMO=1) shows a
                   fictional workspace: no account, no network, safe to screenshot
  login            sign in to a workspace from a terminal; again for another
                   (the picker also offers this)
  logout [W]       forget credentials locally: for
                   workspace W (its URL key or name), or every workspace
  logout --local [W]  alias for local sign-out; API tokens are never revoked
  status           show the workspaces you're signed in to
  tick [--force]   refresh the sidebar labels ($issue, $issue_state,
                   $issue_title); herdr runs this on its events. --force asks
                   Jira again right away
  handoff JSON     (internal) open the popup this one was opened from, once
                   this one has closed
  kickoff W P      (internal) wait for pane P's agent in workspace W, then
                   send it the prompt read from stdin
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-jira:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Println("herdr-jira 0.1.0")
		return nil
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		fmt.Print(usage)
		return nil
	}
	cfg, cfgErr := loadConfig()
	tokenStore = cfg.TokenStore
	// Signing out and checking status must work even with a broken config
	// (they fall back to the defaults); everything else needs a sound one.
	switch {
	case cfgErr == nil:
	case args[0] == "logout" || args[0] == "status" || (len(args) > 1 && args[0] == "action" && args[1] == "logout"):
		fmt.Fprintln(os.Stderr, "herdr-jira: ignoring", cfgErr)
	default:
		if args[0] == "action" {
			notify("Jira", cfgErr.Error())
		}
		return cfgErr
	}

	switch args[0] {
	case "action":
		if len(args) < 2 {
			return errors.New("action needs a name")
		}
		return runAction(ctx, cfg, args[1])
	case "picker":
		demo := os.Getenv("HERDR_JIRA_DEMO") == "1" || (len(args) > 1 && args[1] == "--demo")
		if err := runPicker(ctx, cfg, demo); err != nil {
			return err
		}
		return finishSwap()
	case "login":
		w, err := login(ctx, cfg, cliLoginUI())
		if err != nil {
			return err
		}
		fmt.Printf("Signed in to %s.\n", w.Name)
		return nil
	case "logout":
		local, which, err := logoutArgs(args[1:])
		if err != nil {
			return err
		}
		done, err := logout(ctx, cfg, which, local)
		switch {
		case errors.Is(err, errNotSignedIn):
			fmt.Println("Not signed in.")
			return nil
		case len(done) == 0:
		case local:
			fmt.Printf("Forgot the tokens for %s on this computer. Revoke the API token in Atlassian account settings if needed.\n", strings.Join(done, ", "))
		default:
			fmt.Printf("Signed out of %s: credentials forgotten locally.\n", strings.Join(done, ", "))
		}
		return err
	case "handoff":
		if len(args) != 2 {
			return errors.New("handoff needs a popup")
		}
		return runHandoff(args[1])
	case "tick":
		force := len(args) > 1 && args[1] == "--force"
		if err := tick(ctx, cfg, force); err != nil && !isQuietTickErr(err) {
			return err
		}
		return nil
	case "status":
		return status()
	case "kickoff":
		if len(args) != 3 {
			return errors.New("kickoff needs: workspace pane (and the prompt on stdin)")
		}
		text, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
		if err != nil || len(text) == 0 {
			return errors.New("kickoff: no prompt on stdin")
		}
		return kickoff(cfg, args[1], args[2], string(text))
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// cliLoginUI prints sign-in progress and the link, and reads pasted
// addresses from stdin, a line each.
func cliLoginUI() loginUI {
	pasted := make(chan string)
	prompts := make(chan string)
	go func() {
		defer close(pasted)
		reader := bufio.NewReader(os.Stdin)
		for prompt := range prompts {
			fmt.Println(prompt)
			var line string
			var err error
			if term.IsTerminal(int(os.Stdin.Fd())) {
				var raw []byte
				raw, err = term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				line = string(raw)
			} else {
				line, err = reader.ReadString('\n')
			}
			if err != nil && line == "" {
				return
			}
			pasted <- strings.TrimSpace(line)
		}
	}()
	return loginUI{status: func(s string) { prompts <- s }, pasted: pasted}
}

func runAction(ctx context.Context, cfg config, name string) error {
	switch name {
	case "open":
		inv := invocationContext()
		cwd := inv.WorkspaceCwd
		if cwd == "" {
			cwd = inv.FocusedPaneCwd
		}
		if err := openPicker(map[string]string{"HERDR_JIRA_CWD": cwd}); err != nil {
			notify("Jira", "Couldn't open the picker: "+err.Error())
			return err
		}
		return nil
	case "logout":
		done, err := logout(ctx, cfg, "", false)
		switch {
		case errors.Is(err, errNotSignedIn):
			notify("Jira", "Not signed in.")
		case err != nil && len(done) > 0:
			notify("Jira", "Signed out of "+strings.Join(done, ", ")+". Not signed out: "+err.Error())
			return err
		case err != nil:
			notify("Jira", "Not signed out: "+err.Error())
			return err
		default:
			notify("Jira", "Signed out of "+strings.Join(done, ", ")+": credentials forgotten locally.")
		}
		return nil
	case "demo":
		if err := openPicker(map[string]string{"HERDR_JIRA_DEMO": "1"}); err != nil {
			notify("Jira", "Couldn't open the demo: "+err.Error())
			return err
		}
		return nil
	}
	return fmt.Errorf("unknown action %q", name)
}

// status lists the workspaces you're signed in to. It reads only the
// keychain and the index: no network, no migration.
func status() error {
	ix, err := readIndex()
	if err != nil {
		return err
	}
	signedIn := false
	for _, w := range ix.Workspaces {
		_, err := readStore(account(w.ID))
		if errors.Is(err, errSignedOut) {
			continue
		} else if err != nil {
			return err
		}
		signedIn = true
		fmt.Printf("Signed in to %s (%s). Credentials kept in %s.\n", w.Name, w.URLKey, tokenPlace(account(w.ID)))
	}

	if !signedIn {
		fmt.Println("Not signed in.")
	}
	return nil
}

// logoutArgs reads `logout [--local] [workspace]`, in either order. Signing
// out can't be undone, so anything else is refused rather than guessed at.
func logoutArgs(args []string) (local bool, which string, err error) {
	for _, a := range args {
		switch {
		case a == "--local":
			local = true
		case strings.HasPrefix(a, "-"):
			return false, "", fmt.Errorf("logout: unknown option %q", a)
		case which != "":
			return false, "", errors.New("logout: name one workspace, or none for all of them")
		default:
			which = a
		}
	}
	return local, which, nil
}
