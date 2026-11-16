package main

// /login and /logout. The connector can't run an interactive login itself: it
// hands out a command ("claude auth login ...") for the client to run in a
// terminal. We run it in the user's terminal, wait for that window, then restart
// the account's connector so it picks up the new credentials.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Meta        struct {
		TerminalAuth *struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"terminal-auth"`
	} `json:"_meta"`
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// handleLocalCommand runs commands ccdesk implements itself. Returns true if text was one.
// Called with the app lock held.
func (app *App) handleLocalCommand(s *Session, text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/login":
		method := "claude-ai-login"
		if len(fields) > 1 && strings.EqualFold(fields[1], "console") {
			method = "console-login"
		}
		app.login(s.Acct, method, func(text string, isErr bool) {
			k := KNotice
			if isErr {
				k = KError
			}
			s.add(&Item{Kind: k, Text: text})
		})
		return true
	case "/logout":
		app.logout(s)
		return true
	}
	return false
}

// login runs the account's login command in a terminal. note reports progress
// (called with the app lock held).
func (app *App) login(a *Account, methodID string, note func(text string, isErr bool)) {
	if a.loggingIn {
		note("A login for "+a.Cfg.Name+" is already open in a terminal.", false)
		return
	}
	var m *AuthMethod
	for i := range a.AuthMethods {
		if a.AuthMethods[i].ID == methodID || (methodID == "claude-ai-login" && a.AuthMethods[i].ID == "claude-login") {
			m = &a.AuthMethods[i]
			break
		}
	}
	if m == nil || m.Meta.TerminalAuth == nil {
		note("The connector didn't offer a login method ("+methodID+").", true)
		return
	}
	// Pin the account explicitly: a terminal may be an already-running instance
	// that doesn't inherit our environment.
	envPart := "env -u CLAUDE_CONFIG_DIR"
	if a.Cfg.ConfigDir != "" {
		envPart = "env CLAUDE_CONFIG_DIR=" + shQuote(expandHome(a.Cfg.ConfigDir))
	}
	args := m.Meta.TerminalAuth.Args
	if os.Getenv("CCDESK_DEBUG_LOGIN") != "" { // test: harmless command instead of a real login
		args = []string{"--cli", "auth", "status"}
	}
	cmd := envPart + " " + shQuote(m.Meta.TerminalAuth.Command)
	for _, arg := range args {
		cmd += " " + shQuote(arg)
	}
	marker := filepath.Join(os.TempDir(), fmt.Sprintf("ccdesk-login-%d-%d", os.Getpid(), time.Now().UnixNano()))
	script := fmt.Sprintf(`printf '\033[1mccdesk: logging in to the %s account\033[0m\n\n'; %s; st=$?; echo; if [ $st -eq 0 ]; then echo "Done. You can close this window (Enter)."; else echo "Login failed (exit $st). Press Enter to close."; fi; touch %s; read _`,
		a.Cfg.Name, cmd, shQuote(marker))
	term := exec.Command("xdg-terminal-exec", "sh", "-c", script)
	term.Env = a.env
	if err := term.Start(); err != nil {
		note("Could not open a terminal for login: "+err.Error(), true)
		return
	}
	go term.Wait()
	a.loggingIn = true
	note("Opened a terminal to log in to the "+a.Cfg.Name+" account. Finish there; ccdesk reconnects when it's done.", false)
	go func() {
		deadline := time.Now().Add(20 * time.Minute)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				os.Remove(marker)
				break
			}
			time.Sleep(time.Second)
		}
		app.mu.Lock()
		a.loggingIn = false
		app.mu.Unlock()
		app.reconnect(a, "Reconnected the "+a.Cfg.Name+" account.")
	}()
}

func (app *App) logout(s *Session) {
	a := s.Acct
	s.add(&Item{Kind: KNotice, Text: "Logging out of the " + a.Cfg.Name + " account…"})
	go func() {
		err := a.client.Call("auth/logout", map[string]any{}, nil)
		app.mu.Lock()
		if err != nil {
			s.add(&Item{Kind: KError, Text: "Logout failed: " + err.Error()})
		} else {
			a.Plan = ""
			s.add(&Item{Kind: KNotice, Text: "Logged out of " + a.Cfg.Name + ". Type /login to sign in again."})
		}
		app.mu.Unlock()
		app.invalidate()
	}()
}

// reconnect restarts an account's connector and reopens its active session.
func (app *App) reconnect(a *Account, note string) {
	app.mu.Lock()
	old := a.client
	var reopen *SessionInfo
	for k, s := range app.sessions {
		if s.Acct != a {
			continue
		}
		delete(app.sessions, k)
		if app.active == s && s.ID != "" {
			reopen = &SessionInfo{ID: s.ID, CWD: s.CWD, Title: s.Title, Acct: a}
			app.active = nil
			app.draftAcct = a
		}
	}
	a.Plan = ""
	app.mu.Unlock()
	if old != nil {
		old.Close()
	}
	app.startAccount(a) // also refreshes the session list
	app.mu.Lock()
	if reopen != nil {
		if app.active == nil {
			app.draftAcct = nil
			app.openSession(reopen)
		}
		if s := app.findSession(a, reopen.ID); s != nil {
			if s.Loading {
				s.pendingNote = note
			} else {
				s.add(&Item{Kind: KNotice, Text: note})
			}
		}
	}
	app.mu.Unlock()
	app.invalidate()
}
