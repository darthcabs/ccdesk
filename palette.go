package main

// Built-in themes (auto / light / dark) and per-account accent colors, so ccdesk
// works without Omarchy. Omarchy themes are offered as well when installed.

import (
	"image/color"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// accentPresets are offered in the theme picker and suggested for new accounts.
var accentPresets = []string{"#3b82f6", "#e8743b", "#22a06b", "#8b5cf6", "#e0457b", "#0ea5a4", "#d99a00", "#e5484d"}

func isBuiltinTheme(name string) bool { return name == "auto" || name == "light" || name == "dark" }

// hasOmarchy reports whether Omarchy themes are available on this machine.
func hasOmarchy() bool {
	_, err := os.Stat(currentThemeDir())
	return err == nil && len(ListThemes()) > 0
}

// defaultTheme is the theme for a fresh account: Omarchy's current theme when
// running on Omarchy, otherwise follow the system's light/dark preference.
func defaultTheme() string {
	if hasOmarchy() {
		return "current"
	}
	return "auto"
}

var darkCache struct {
	sync.Mutex
	at   time.Time
	dark bool
}

// systemDark reports the desktop's light/dark preference (freedesktop/GNOME
// color-scheme setting), cached for a couple of seconds.
func systemDark() bool {
	darkCache.Lock()
	defer darkCache.Unlock()
	if time.Since(darkCache.at) < 2*time.Second {
		return darkCache.dark
	}
	darkCache.at = time.Now()
	out, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
	if err == nil {
		darkCache.dark = strings.Contains(string(out), "dark")
	} else {
		darkCache.dark = strings.Contains(strings.ToLower(os.Getenv("GTK_THEME")), "dark")
	}
	return darkCache.dark
}

func hex(s string) color.NRGBA {
	c, _ := parseHex(s)
	return c
}

func builtinPalette(dark bool) Palette {
	if dark {
		return Palette{
			Name: "dark", Dark: true,
			Bg: hex("#1e1f22"), Side: hex("#18191b"), Deep: hex("#121314"), Card: hex("#2a2c30"),
			Fg: hex("#d4d4d4"), Dim: hex("#8b8f96"), Bright: hex("#f2f2f2"),
			Accent: hex(accentPresets[0]), Sel: hex("#34373d"), Muted: hex("#4a4d52"),
			Red: hex("#f47067"), Green: hex("#57ab5a"), Yellow: hex("#c69026"), Magenta: hex("#b083f0"), Cyan: hex("#39c5cf"),
		}
	}
	return Palette{
		Name: "light",
		Bg:   hex("#fafafa"), Side: hex("#f0f0f1"), Deep: hex("#e6e7e9"), Card: hex("#efeff1"),
		Fg: hex("#24292f"), Dim: hex("#6e7781"), Bright: hex("#000000"),
		Accent: hex(accentPresets[0]), Sel: hex("#dde3ec"), Muted: hex("#c8ccd2"),
		Red: hex("#cf222e"), Green: hex("#1a7f37"), Yellow: hex("#9a6700"), Magenta: hex("#8250df"), Cyan: hex("#1b7c83"),
	}
}

// paletteFor resolves an account's theme and accent into colors.
func paletteFor(c AccountConfig) Palette {
	p := LoadPalette(c.Theme)
	if c.Accent != "" {
		if acc, ok := parseHex(c.Accent); ok {
			p = withAccent(p, acc)
		}
	}
	return p
}

func withAccent(p Palette, acc color.NRGBA) Palette {
	p.Accent = acc
	p.OnAcc = onColor(acc)
	if isBuiltinTheme(p.Name) || p.Name == "light" || p.Name == "dark" {
		p.Sel = mix(p.Bg, acc, 0.2)
	}
	return p
}

func onColor(c color.NRGBA) color.NRGBA {
	if luminance(c) > 0.6 {
		return color.NRGBA{0x11, 0x11, 0x11, 0xff}
	}
	return color.NRGBA{0xff, 0xff, 0xff, 0xff}
}

// suggestAccent picks a preset accent no other account uses.
func suggestAccent(used []AccountConfig) string {
	taken := map[string]bool{}
	for _, a := range used {
		taken[strings.ToLower(a.Accent)] = true
	}
	for _, c := range accentPresets {
		if !taken[c] {
			return c
		}
	}
	return accentPresets[0]
}
