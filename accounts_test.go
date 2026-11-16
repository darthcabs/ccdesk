package main

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeHome(t *testing.T) string {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	return h
}

func TestDetectSingle(t *testing.T) {
	h := fakeHome(t)
	os.MkdirAll(filepath.Join(h, ".claude", "projects"), 0o755)
	got := detectAccounts()
	if len(got) != 1 || got[0].ConfigDir != "" || got[0].Name != "Main" {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectSecondAccount(t *testing.T) {
	h := fakeHome(t)
	os.MkdirAll(filepath.Join(h, ".claude", "projects"), 0o755)
	os.MkdirAll(filepath.Join(h, ".claude-work"), 0o755)
	os.WriteFile(filepath.Join(h, ".claude-work", ".credentials.json"), []byte("{}"), 0o600)
	os.MkdirAll(filepath.Join(h, ".claude-junk"), 0o755) // empty: not a Claude folder
	got := detectAccounts()
	if len(got) != 2 || got[0].Name != "Personal" || got[1].Name != "Work" || got[1].ConfigDir != "~/.claude-work" {
		t.Fatalf("got %+v", got)
	}
}

func TestDraftConfig(t *testing.T) {
	fakeHome(t)
	d := &acctDraft{}
	d.name = newEditor("My Work")
	d.dir = newEditor("")
	d.defaultDir = newEditor("")
	c := d.config()
	if c.ConfigDir != "~/.claude-my-work" || c.DefaultDir != "~" || c.Theme != "current" {
		t.Fatalf("new account: %+v", c)
	}
	// pointing an account at ~/.claude must keep CLAUDE_CONFIG_DIR unset
	d.dir = newEditor("~/.claude/")
	if c := d.config(); c.ConfigDir != "" {
		t.Fatalf("default folder stored as %q", c.ConfigDir)
	}
}
