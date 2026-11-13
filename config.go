package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type AccountConfig struct {
	Name string `toml:"name"`
	// ConfigDir is passed as CLAUDE_CONFIG_DIR. Empty = Claude's default (~/.claude).
	ConfigDir string `toml:"config_dir"`
	// Theme is an Omarchy theme name, or "current" to follow the system theme.
	Theme string `toml:"theme"`
	// DefaultDir is where new sessions start.
	DefaultDir string `toml:"default_dir"`
}

type Config struct {
	Font      string  `toml:"font"`
	MonoFont  string  `toml:"mono_font"`
	FontSize  float32 `toml:"font_size"`
	UIScale   float32 `toml:"ui_scale"` // extra zoom factor; 0 or 1 = normal
	Connector string  `toml:"connector"`
	// HiddenOptions lists session settings (by name or id) not shown as dropdowns.
	HiddenOptions []string `toml:"hidden_options"`
	// PromptSuggestions shows Claude's predicted next prompt in the message box.
	PromptSuggestions *bool           `toml:"prompt_suggestions"`
	Accounts          []AccountConfig `toml:"account"`
}

func configPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home(), ".config")
	}
	return filepath.Join(dir, "ccdesk", "config.toml")
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func expandHome(p string) string {
	if p == "~" {
		return home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), p[2:])
	}
	return p
}

func shortenHome(p string) string {
	h := home()
	if p == h {
		return "~"
	}
	if strings.HasPrefix(p, h+"/") {
		return "~" + p[len(h):]
	}
	return p
}

func defaultConfig() Config {
	return Config{
		Font:          "Ubuntu",
		MonoFont:      "Ubuntu Mono",
		FontSize:      18,
		Connector:     "~/.local/lib/claude-agent-acp/current/claude-agent-acp",
		HiddenOptions: []string{"Fast mode", "Agent"},
		Accounts: []AccountConfig{
			{Name: "Personal", ConfigDir: "", Theme: "current", DefaultDir: "~/Work"},
			{Name: "Work", ConfigDir: "~/.claude-work", Theme: "tokyo-night", DefaultDir: "~/Work"},
		},
	}
}

func loadConfig() Config {
	cfg := defaultConfig()
	b, err := os.ReadFile(configPath())
	if err != nil {
		saveConfig(cfg)
		return cfg
	}
	var c Config
	dirty := false
	if _, err := toml.Decode(string(b), &c); err != nil {
		return cfg
	}
	if c.Font == "" {
		c.Font = cfg.Font
	}
	if c.MonoFont == "" {
		c.MonoFont = cfg.MonoFont
	}
	if c.FontSize == 0 {
		c.FontSize = cfg.FontSize
	}
	if c.Connector == "" {
		c.Connector = cfg.Connector
	}
	if len(c.Accounts) == 0 {
		c.Accounts = cfg.Accounts
	}
	if c.PromptSuggestions == nil {
		t := true
		c.PromptSuggestions = &t
		dirty = true
	}
	if c.HiddenOptions == nil {
		c.HiddenOptions = cfg.HiddenOptions
		dirty = true
	}
	for i := range c.Accounts {
		if c.Accounts[i].Theme == "" {
			c.Accounts[i].Theme = "current"
		}
		if c.Accounts[i].DefaultDir == "" {
			c.Accounts[i].DefaultDir = "~"
		}
	}
	if dirty {
		saveConfig(c)
	}
	return c
}

func saveConfig(c Config) error {
	p := configPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	var buf bytes.Buffer
	buf.WriteString("# ccdesk configuration. Themes are Omarchy theme names (see the theme picker),\n# or \"current\" to follow the active Omarchy theme.\n\n")
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o644)
}

func (c Config) suggestionsOn() bool { return c.PromptSuggestions == nil || *c.PromptSuggestions }
