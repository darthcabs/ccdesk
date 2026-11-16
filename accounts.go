package main

// Accounts screen: add, rename, re-point and remove Claude accounts, and log in.
// An account is just a Claude config folder (CLAUDE_CONFIG_DIR); removing one
// only takes it out of ccdesk, never deletes the folder.

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type acctDraft struct {
	acct       *Account // nil for an account added on this screen
	name       widget.Editor
	dir        widget.Editor
	defaultDir widget.Editor
	theme      string
	removed    bool

	themeBtn, loginBtn, removeBtn widget.Clickable
}

type accountsScreen struct {
	open    bool
	drafts  []*acctDraft
	err     string
	list    widget.List
	addBtn  widget.Clickable
	saveBtn widget.Clickable
	cancel  widget.Clickable
	gear    widget.Clickable
}

func newEditor(text string) widget.Editor {
	e := widget.Editor{SingleLine: true}
	e.SetText(text)
	return e
}

func (u *UI) openAccounts() {
	sc := &u.accts
	sc.open = true
	sc.err = ""
	sc.drafts = nil
	sc.list.Axis = layout.Vertical
	for _, a := range u.app.accounts {
		d := &acctDraft{acct: a, theme: a.Cfg.Theme}
		d.name = newEditor(a.Cfg.Name)
		d.dir = newEditor(a.Cfg.ConfigDir)
		d.defaultDir = newEditor(a.Cfg.DefaultDir)
		sc.drafts = append(sc.drafts, d)
	}
}

func (u *UI) addAccountDraft() {
	sc := &u.accts
	var used []AccountConfig
	for _, d := range sc.drafts {
		used = append(used, AccountConfig{Theme: d.theme})
	}
	d := &acctDraft{theme: suggestTheme(used)}
	name := "Work"
	for _, x := range sc.drafts {
		if strings.EqualFold(x.name.Text(), name) {
			name = fmt.Sprintf("Account %d", len(sc.drafts)+1)
		}
	}
	d.name = newEditor(name)
	d.dir = newEditor("")
	d.defaultDir = newEditor("~")
	sc.drafts = append(sc.drafts, d)
	sc.list.Position.First = len(sc.drafts) // scroll to it
}

// draftConfig turns a draft into a config entry (empty folder for a new account = ~/.claude-<name>).
func (d *acctDraft) config() AccountConfig {
	c := AccountConfig{
		Name:       strings.TrimSpace(d.name.Text()),
		ConfigDir:  strings.TrimSpace(d.dir.Text()),
		DefaultDir: strings.TrimSpace(d.defaultDir.Text()),
		Theme:      d.theme,
	}
	if c.ConfigDir == "" && d.acct == nil {
		c.ConfigDir = "~/.claude-" + firstNonEmpty(slug(c.Name), "account")
	}
	// Claude's default folder must stay "unset": with CLAUDE_CONFIG_DIR set,
	// Claude also moves ~/.claude.json inside the folder and would look logged out.
	if filepath.Clean(expandHome(c.ConfigDir)) == filepath.Join(home(), ".claude") {
		c.ConfigDir = ""
	}
	if c.DefaultDir == "" {
		c.DefaultDir = "~"
	}
	if c.Theme == "" {
		c.Theme = "current"
	}
	return c
}

func (u *UI) saveAccounts() {
	sc := &u.accts
	var keep []*acctDraft
	var cfgs []AccountConfig
	names := map[string]bool{}
	dirs := map[string]string{}
	for _, d := range sc.drafts {
		if d.removed {
			continue
		}
		c := d.config()
		if c.Name == "" {
			sc.err = "Every account needs a name."
			return
		}
		if names[strings.ToLower(c.Name)] {
			sc.err = "Two accounts are called \"" + c.Name + "\"."
			return
		}
		names[strings.ToLower(c.Name)] = true
		if other, ok := dirs[c.claudeDir()]; ok {
			sc.err = c.Name + " and " + other + " use the same Claude folder."
			return
		}
		dirs[c.claudeDir()] = c.Name
		keep = append(keep, d)
		cfgs = append(cfgs, c)
	}
	if len(keep) == 0 {
		sc.err = "Keep at least one account."
		return
	}
	u.app.applyAccounts(keep, cfgs)
	u.filterIdx = -1
	u.draft = nil
	sc.open = false
}

// applyAccounts swaps in the edited account list (called with the app lock held).
func (app *App) applyAccounts(drafts []*acctDraft, cfgs []AccountConfig) {
	kept := map[*Account]bool{}
	for _, d := range drafts {
		if d.acct != nil {
			kept[d.acct] = true
		}
	}
	// drop removed accounts
	for _, a := range app.accounts {
		if kept[a] {
			continue
		}
		for k, s := range app.sessions {
			if s.Acct == a {
				delete(app.sessions, k)
			}
		}
		if app.active != nil && app.active.Acct == a {
			app.active = nil
		}
		if app.draftAcct == a {
			app.draftAcct = nil
		}
		if a.client != nil {
			go a.client.Close()
		}
	}
	app.cfg.Accounts = cfgs
	var accts []*Account
	var restart, start []*Account
	for i, d := range drafts {
		c := &app.cfg.Accounts[i]
		a := d.acct
		if a == nil {
			a = &Account{}
			start = append(start, a)
			os.MkdirAll(c.claudeDir(), 0o700)
		} else if a.Cfg.claudeDir() != c.claudeDir() {
			restart = append(restart, a)
		}
		if a.Cfg == nil || a.Cfg.Theme != c.Theme {
			a.Pal = LoadPalette(c.Theme)
		}
		a.Idx = i
		a.Cfg = c
		accts = append(accts, a)
	}
	app.accounts = accts
	saveConfig(app.cfg)
	for _, a := range restart {
		go app.reconnect(a, "Reconnected "+a.Cfg.Name+" with its new folder.")
	}
	for _, a := range start {
		a := a
		go func() {
			app.startAccount(a)
			// a brand-new folder has no login yet: open the login right away
			if _, err := os.Stat(filepath.Join(a.Cfg.claudeDir(), ".credentials.json")); err != nil {
				app.mu.Lock()
				if a.Ready {
					app.login(a, "claude-ai-login", func(text string, isErr bool) { a.LoginNote = text })
				}
				app.mu.Unlock()
				app.invalidate()
			}
		}()
	}
}

// ---------------------------------------------------------------------------
// Layout

func (u *UI) layoutAccounts(gtx C) D {
	app := u.app
	sc := &u.accts
	if sc.cancel.Clicked(gtx) {
		sc.open = false
		return D{}
	}
	if sc.addBtn.Clicked(gtx) {
		u.addAccountDraft()
	}
	if sc.saveBtn.Clicked(gtx) {
		u.saveAccounts()
		if !sc.open {
			return D{}
		}
	}
	live := 0
	for _, d := range sc.drafts {
		if !d.removed {
			live++
		}
	}
	for _, d := range sc.drafts {
		if d.removeBtn.Clicked(gtx) {
			if d.removed || live > 1 {
				d.removed = !d.removed
			}
		}
		if d.themeBtn.Clicked(gtx) && d.acct != nil {
			u.openPicker(d.acct)
			u.pickerDraft = d
		}
		if d.loginBtn.Clicked(gtx) && d.acct != nil {
			a := d.acct
			app.login(a, "claude-ai-login", func(text string, isErr bool) { a.LoginNote = text })
		}
	}

	return layout.Inset{Top: 16, Left: 22, Right: 22, Bottom: 16}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, u.label("Accounts", u.size*1.3, u.p.Bright, true).Layout),
					layout.Rigid(func(gtx C) D { return u.button(gtx, &sc.cancel, "Cancel", u.p.Bg, u.p.Dim, true) }),
					layout.Rigid(layout.Spacer{Width: 8}.Layout),
					layout.Rigid(func(gtx C) D { return u.button(gtx, &sc.saveBtn, "Save", u.p.Accent, u.p.OnAcc, false) }),
				)
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: 4, Bottom: 12}.Layout(gtx, u.label(
					"Each account is a Claude Code config folder with its own login and history. "+
						"Removing an account only removes it from ccdesk; its folder is left alone.",
					u.size*0.8, u.p.Dim, false).Layout)
			}),
			layout.Rigid(func(gtx C) D {
				if sc.err == "" {
					return D{}
				}
				return layout.Inset{Bottom: 10}.Layout(gtx, u.label(sc.err, u.size*0.85, u.p.Red, true).Layout)
			}),
			layout.Flexed(1, func(gtx C) D {
				l := material.List(u.th, &sc.list)
				return l.Layout(gtx, len(sc.drafts)+1, func(gtx C, i int) D {
					return u.centered(gtx, func(gtx C) D {
						if i == len(sc.drafts) {
							return layout.Inset{Top: 4}.Layout(gtx, func(gtx C) D {
								return u.button(gtx, &sc.addBtn, "+ Add account", u.p.Bg, u.p.Accent, true)
							})
						}
						return u.accountCard(gtx, sc.drafts[i])
					})
				})
			}),
		)
	})
}

func (u *UI) accountCard(gtx C, d *acctDraft) D {
	pal := LoadPaletteCached(u, d.theme)
	return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return widget.Border{Color: withAlpha(pal.Accent, 0xa0), CornerRadius: 8, Width: 1.5}.Layout(gtx, func(gtx C) D {
			return bgBox(gtx, u.p.Side, 8, func(gtx C) D {
				return layout.UniformInset(14).Layout(gtx, func(gtx C) D {
					if d.removed {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, u.label(d.name.Text()+" will be removed from ccdesk when you save.", u.size*0.9, u.p.Dim, false).Layout),
							layout.Rigid(func(gtx C) D { return u.button(gtx, &d.removeBtn, "Undo", u.p.Side, u.p.Fg, true) }),
						)
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						// name, status, actions
						layout.Rigid(func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx C) D {
									s := gtx.Dp(12)
									fillRect(gtx, pal.Accent, image.Pt(s, s), s/2)
									return D{Size: image.Pt(s+gtx.Dp(10), s)}
								}),
								layout.Flexed(1, func(gtx C) D {
									return u.field(gtx, &d.name, "Account name", u.size*1.05, false, true)
								}),
								layout.Rigid(layout.Spacer{Width: 12}.Layout),
								layout.Rigid(u.label(u.acctStatus(d), u.size*0.78, u.p.Dim, false).Layout),
							)
						}),
						layout.Rigid(layout.Spacer{Height: 10}.Layout),
						layout.Rigid(func(gtx C) D {
							hint := "~/.claude (Claude's default)"
							if d.acct == nil {
								hint = "~/.claude-" + firstNonEmpty(slug(d.name.Text()), "account") + " (created for you)"
							}
							return u.labeledField(gtx, "Claude folder", &d.dir, hint)
						}),
						layout.Rigid(layout.Spacer{Height: 6}.Layout),
						layout.Rigid(func(gtx C) D {
							return u.labeledField(gtx, "New sessions in", &d.defaultDir, "~")
						}),
						layout.Rigid(layout.Spacer{Height: 10}.Layout),
						layout.Rigid(func(gtx C) D {
							var kids []layout.FlexChild
							btn := func(b *widget.Clickable, txt string) layout.FlexChild {
								return layout.Rigid(func(gtx C) D {
									return layout.Inset{Right: 8}.Layout(gtx, func(gtx C) D { return u.button(gtx, b, txt, u.p.Side, u.p.Fg, true) })
								})
							}
							if d.acct != nil {
								kids = append(kids, btn(&d.themeBtn, "Theme: "+d.acct.Cfg.Theme), btn(&d.loginBtn, "Log in"))
							} else {
								kids = append(kids, layout.Rigid(u.label("Theme: "+d.theme+" · save, then log in", u.size*0.78, u.p.Dim, false).Layout))
							}
							kids = append(kids, layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }))
							kids = append(kids, layout.Rigid(func(gtx C) D { return u.button(gtx, &d.removeBtn, "Remove", u.p.Side, u.p.Red, true) }))
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
						}),
						layout.Rigid(func(gtx C) D {
							if d.acct == nil || d.acct.LoginNote == "" {
								return D{}
							}
							return layout.Inset{Top: 8}.Layout(gtx, u.label(d.acct.LoginNote, u.size*0.78, u.p.Dim, false).Layout)
						}),
					)
				})
			})
		})
	})
}

func (u *UI) acctStatus(d *acctDraft) string {
	a := d.acct
	switch {
	case a == nil:
		return "new"
	case a.loggingIn:
		return "logging in…"
	case a.Status == "starting":
		return "connecting…"
	case a.Status != "":
		return a.Status
	case a.Plan != "":
		return "signed in · " + a.Plan
	}
	return "not signed in"
}

func (u *UI) labeledField(gtx C, label string, ed *widget.Editor, hint string) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Dp(150)
			return u.label(label, u.size*0.8, u.p.Dim, true).Layout(gtx)
		}),
		layout.Flexed(1, func(gtx C) D { return u.field(gtx, ed, hint, u.size*0.9, true, false) }),
	)
}

func (u *UI) field(gtx C, ed *widget.Editor, hint string, size float32, mono, bold bool) D {
	border := withAlpha(u.p.Dim, 0x70)
	if gtx.Focused(ed) {
		border = u.p.Accent
	}
	return widget.Border{Color: border, CornerRadius: 6, Width: 1}.Layout(gtx, func(gtx C) D { return u.fieldInner(gtx, ed, hint, size, mono, bold) })
}

func (u *UI) fieldInner(gtx C, ed *widget.Editor, hint string, size float32, mono, bold bool) D {
	return bgBox(gtx, u.p.Deep, 6, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Top: 5, Bottom: 5, Left: 10, Right: 10}.Layout(gtx, func(gtx C) D {
			pointer.CursorText.Add(gtx.Ops)
			e := material.Editor(u.th, ed, hint)
			e.Font = u.font(bold, false, mono)
			e.TextSize = sp(size)
			e.Color = u.p.Fg
			e.HintColor = withAlpha(u.p.Dim, 0xb0)
			e.SelectionColor = withAlpha(u.p.Accent, 0x60)
			return e.Layout(gtx)
		})
	})
}

func LoadPaletteCached(u *UI, name string) Palette {
	if p, ok := u.palCache[name]; ok && name != "current" {
		return p
	}
	p := LoadPalette(name)
	u.palCache[name] = p
	return p
}
