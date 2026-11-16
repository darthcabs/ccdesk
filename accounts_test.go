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
	// without Omarchy: both follow the system, and the second gets its own accent
	if got[0].Theme != "auto" || got[1].Theme != "auto" || got[1].Accent == "" || got[1].Accent == accentPresets[0] {
		t.Fatalf("themes: %+v", got)
	}
}

func TestDraftConfig(t *testing.T) {
	fakeHome(t)
	d := &acctDraft{}
	d.name = newEditor("My Work")
	d.dir = newEditor("")
	d.defaultDir = newEditor("")
	c := d.config()
	if c.ConfigDir != "~/.claude-my-work" || c.DefaultDir != "~" || c.Theme != "auto" {
		t.Fatalf("new account: %+v", c)
	}
	// pointing an account at ~/.claude must keep CLAUDE_CONFIG_DIR unset
	d.dir = newEditor("~/.claude/")
	if c := d.config(); c.ConfigDir != "" {
		t.Fatalf("default folder stored as %q", c.ConfigDir)
	}
}

func TestAccentOverride(t *testing.T) {
	p := paletteFor(AccountConfig{Theme: "dark", Accent: "#22a06b"})
	if p.Accent != hex("#22a06b") || !p.Dark {
		t.Fatalf("got %+v", p)
	}
	if p.OnAcc != onColor(hex("#22a06b")) {
		t.Fatal("text-on-accent color not recomputed")
	}
}

func TestRedIsRed(t *testing.T) {
	if _, err := os.Stat("/usr/share/omarchy/themes/hackerman/colors.toml"); err != nil {
		t.Skip("Omarchy themes not installed")
	}
	p := LoadPalette("hackerman") // its "red" slot is green
	if p.Red.R < 0xc0 || p.Red.G > 0x80 {
		t.Fatalf("red is %v", p.Red)
	}
}
