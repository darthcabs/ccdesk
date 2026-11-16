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
	// Theme is "auto" (follow the system's light/dark setting), "light", "dark",
	// an Omarchy theme name, or "current" (Omarchy's active theme).
	Theme string `toml:"theme"`
	// Accent overrides the theme's accent color ("#rrggbb"); empty = the theme's own.
	Accent string `toml:"accent,omitempty"`
	// DefaultDir is where new sessions start.
	DefaultDir string `toml:"default_dir"`
}

type Config struct {
	Font     string  `toml:"font"`
	MonoFont string  `toml:"mono_font"`
	FontSize float32 `toml:"font_size"`
	// PromptFontSize is the message box's text size; 0 = font_size - 2.
	PromptFontSize float32 `toml:"prompt_font_size"`
	UIScale        float32 `toml:"ui_scale"` // extra zoom factor; 0 or 1 = normal
	Connector      string  `toml:"connector"`
	// HiddenOptions lists session settings (by name or id) not shown as dropdowns.
	HiddenOptions []string `toml:"hidden_options"`
	// PromptSuggestions shows Claude's predicted next prompt in the message box.
	PromptSuggestions *bool `toml:"prompt_suggestions"`
	// ShowThinking asks Claude for thinking summaries and shows them live.
	ShowThinking *bool           `toml:"show_thinking"`
	Accounts     []AccountConfig `toml:"account"`
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
		Accounts:      detectAccounts(),
	}
}

// accentThemes are suggested for extra accounts, so each account looks different.
var accentThemes = []string{"tokyo-night", "everforest", "gruvbox", "rose-pine", "kanagawa", "nord", "catppuccin"}

// suggestTheme picks an installed theme not used by any of the given accounts.
func suggestTheme(used []AccountConfig) string {
	if !hasOmarchy() {
		return defaultTheme() // built-in theme; accounts differ by accent instead
	}
	have := map[string]bool{}
	for _, t := range ListThemes() {
		have[t] = true
	}
	taken := map[string]bool{}
	for _, a := range used {
		taken[a.Theme] = true
	}
	for _, t := range accentThemes {
		if have[t] && !taken[t] {
			return t
		}
	}
	return defaultTheme()
}

// isClaudeDir reports whether dir looks like a Claude Code config folder.
func isClaudeDir(dir string) bool {
	for _, f := range []string{".credentials.json", "projects", "settings.json", "history.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// detectAccounts builds the first-run account list: Claude's default folder,
// plus any ~/.claude-<name> folders (the usual way to keep a second account).
func detectAccounts() []AccountConfig {
	main := AccountConfig{Name: "Main", Theme: defaultTheme(), DefaultDir: "~"}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" && filepath.Clean(expandHome(d)) != filepath.Join(home(), ".claude") {
		main.ConfigDir = shortenHome(d)
	}
	accts := []AccountConfig{main}
	matches, _ := filepath.Glob(filepath.Join(home(), ".claude-*"))
	for _, m := range matches {
		if st, err := os.Stat(m); err != nil || !st.IsDir() || !isClaudeDir(m) {
			continue
		}
		suffix := strings.TrimPrefix(filepath.Base(m), ".claude-")
		if suffix == "" || shortenHome(m) == main.ConfigDir {
			continue
		}
		name := strings.ToUpper(suffix[:1]) + suffix[1:]
		ac := AccountConfig{Name: name, ConfigDir: shortenHome(m), Theme: suggestTheme(accts), DefaultDir: "~"}
		if isBuiltinTheme(ac.Theme) {
			ac.Accent = suggestAccent(append(accts, AccountConfig{Accent: accentPresets[0]}))
		}
		accts = append(accts, ac)
	}
	if len(accts) > 1 {
		accts[0].Name = "Personal"
	}
	return accts
}

// claudeDir is the folder an account's Claude config lives in.
func (a AccountConfig) claudeDir() string {
	if a.ConfigDir == "" {
		return filepath.Join(home(), ".claude")
	}
	return filepath.Clean(expandHome(a.ConfigDir))
}

// slug makes a folder-friendly name ("My Work" -> "my-work").
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
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
	if c.ShowThinking == nil {
		t := true
		c.ShowThinking = &t
		dirty = true
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
			c.Accounts[i].Theme = defaultTheme()
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

func (c Config) thinkingOn() bool { return c.ShowThinking == nil || *c.ShowThinking }
