package main

// Omarchy theme loading: every theme ships a colors.toml with a fixed set of keys.

import (
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type Palette struct {
	Name    string
	Dark    bool
	Bg      color.NRGBA // main background
	Side    color.NRGBA // sidebar
	Deep    color.NRGBA // code blocks, inputs
	Card    color.NRGBA // tool cards, user bubble
	Fg      color.NRGBA
	Dim     color.NRGBA
	Bright  color.NRGBA
	Accent  color.NRGBA
	OnAcc   color.NRGBA // text on accent
	Sel     color.NRGBA
	Muted   color.NRGBA
	Red     color.NRGBA
	Green   color.NRGBA
	Yellow  color.NRGBA
	Magenta color.NRGBA
	Cyan    color.NRGBA
}

func themeDirs() []string {
	return []string{
		filepath.Join(home(), ".config/omarchy/themes"),
		"/usr/share/omarchy/themes",
		filepath.Join(home(), ".local/share/omarchy/themes"),
	}
}

func currentThemeDir() string {
	return filepath.Join(home(), ".local/state/omarchy/current/theme")
}

func currentThemeName() string {
	b, err := os.ReadFile(filepath.Join(home(), ".local/state/omarchy/current/theme.name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ListThemes returns the names of all installed Omarchy themes.
func ListThemes() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range themeDirs() {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if seen[e.Name()] {
				continue
			}
			if _, err := os.Stat(filepath.Join(d, e.Name(), "colors.toml")); err == nil {
				seen[e.Name()] = true
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

func themeDir(name string) string {
	if name == "" || name == "current" {
		return currentThemeDir()
	}
	for _, d := range themeDirs() {
		p := filepath.Join(d, name)
		if _, err := os.Stat(filepath.Join(p, "colors.toml")); err == nil {
			return p
		}
	}
	return currentThemeDir()
}

func LoadPalette(name string) Palette {
	switch name {
	case "auto":
		p := builtinPalette(systemDark())
		p.Name = "auto"
		return p
	case "light", "dark":
		return builtinPalette(name == "dark")
	case "", "current":
		if !hasOmarchy() {
			return builtinPalette(systemDark())
		}
	}
	m := map[string]any{}
	toml.DecodeFile(filepath.Join(themeDir(name), "colors.toml"), &m)
	get := func(k string, def string) color.NRGBA {
		if s, ok := m[k].(string); ok {
			if c, ok := parseHex(s); ok {
				return c
			}
		}
		c, _ := parseHex(def)
		return c
	}
	p := Palette{Name: name}
	if name == "current" || name == "" {
		p.Name = "current"
	}
	mode, _ := m["mode"].(string)
	bg := get("background", "#1a1b26")
	p.Dark = mode != "light"
	if mode == "" {
		p.Dark = luminance(bg) < 0.5
	}
	p.Bg = bg
	p.Side = get("dark_background", "#13141c")
	p.Deep = get("darker_background", "#0e0e14")
	p.Card = get("lighter_background", "#24283b")
	p.Fg = get("foreground", "#a9b1d6")
	p.Dim = get("dark_foreground", "#565f89")
	p.Bright = get("bright_foreground", "#c0caf5")
	p.Accent = get("accent", "#7aa2f7")
	p.Sel = get("selection", "#292e42")
	p.Muted = get("muted", "#414868")
	p.Red = get("red", "#f7768e")
	p.Green = get("green", "#9ece6a")
	p.Yellow = get("yellow", "#e0af68")
	p.Magenta = get("magenta", "#ad8ee6")
	p.Cyan = get("cyan", "#449dab")
	if luminance(p.Accent) > 0.55 {
		p.OnAcc = color.NRGBA{0x11, 0x11, 0x11, 0xff}
	} else {
		p.OnAcc = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	}
	// Some themes have identical background variants; make sure the sidebar and cards stand out.
	if p.Side == p.Bg {
		p.Side = shade(p.Bg, p.Dark, 0.06)
	}
	if p.Card == p.Bg {
		p.Card = shade(p.Bg, !p.Dark, 0.06)
	}
	if p.Deep == p.Bg {
		p.Deep = shade(p.Bg, p.Dark, 0.10)
	}
	// Errors, failures, Stop and Remove must read as red; some themes use their
	// "red" slot for other hues (hackerman's is green), so use a real red.
	p.Red = builtinPalette(p.Dark).Red
	return p
}

func parseHex(s string) (color.NRGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 && len(s) != 8 {
		return color.NRGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	if len(s) == 6 {
		return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}, true
	}
	return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
}

func luminance(c color.NRGBA) float64 {
	return (0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)) / 255
}

// shade darkens (darker=true) or lightens c by amount (0..1).
func shade(c color.NRGBA, darker bool, amt float64) color.NRGBA {
	f := func(v uint8) uint8 {
		if darker {
			return uint8(float64(v) * (1 - amt))
		}
		return uint8(float64(v) + (255-float64(v))*amt)
	}
	return color.NRGBA{f(c.R), f(c.G), f(c.B), c.A}
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return color.NRGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 0xff}
}
