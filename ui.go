package main

import (
	"fmt"
	"image"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

type UI struct {
	app    *App
	shaper *text.Shaper
	th     *material.Theme
	p      Palette // palette of the active account (set every frame)

	sans, mono font.Typeface
	size       float32

	sideList   widget.List
	search     widget.Editor
	filterIdx  int // -1 = all accounts
	filterAll  widget.Clickable
	refreshBtn widget.Clickable

	editor     widget.Editor
	dirEd      widget.Editor
	sendBtn    widget.Clickable
	stopBtn    widget.Clickable
	modeBtn    widget.Clickable
	pending    []*Attachment
	draft      *Session
	lastActive *Session

	// theme picker
	pickerAcct  *Account
	pickerList  widget.List
	pickerClose widget.Clickable
	themeNames  []string
	themeBtns   map[string]*widget.Clickable
	palCache    map[string]Palette

	welcomeList widget.List
	selOwner    *msgText

	// slash-command popup
	slashIdx    int
	slashHidden string // editor text for which the popup was dismissed
	slashBtns   []*widget.Clickable

	// dropdown menus (model, mode, ...)
	ddOpen  string
	ddBtns  map[string]*widget.Clickable
	ddItems map[string]*widget.Clickable

	// drag and drop
	dragOver   bool
	editorH    int
	testCopied bool
	sideMargin int // empty space beside the centred column (text selection extends into it) // height of the editor box last frame (for anchoring the slash popup)
}

func newUI(app *App) *UI {
	u := &UI{app: app, filterIdx: -1, themeBtns: map[string]*widget.Clickable{}, palCache: map[string]Palette{},
		ddBtns: map[string]*widget.Clickable{}, ddItems: map[string]*widget.Clickable{}}
	u.shaper = text.NewShaper()
	u.th = material.NewTheme()
	u.th.Shaper = u.shaper
	u.sans = font.Typeface(app.cfg.Font)
	u.mono = font.Typeface(app.cfg.MonoFont)
	u.th.Face = u.sans
	u.size = app.cfg.FontSize
	u.sideList.Axis = layout.Vertical
	u.pickerList.Axis = layout.Vertical
	u.welcomeList.Axis = layout.Vertical
	u.search.SingleLine = true
	u.dirEd.SingleLine = true
	u.editor.Submit = true
	return u
}

// ---------------------------------------------------------------------------
// helpers

func sp(v float32) unit.Sp { return unit.Sp(v) }

func (u *UI) font(bold, italic, mono bool) font.Font {
	f := font.Font{Typeface: u.sans}
	if mono {
		f.Typeface = u.mono
	}
	if bold {
		f.Weight = font.Bold
	}
	if italic {
		f.Style = font.Italic
	}
	return f
}

func (u *UI) label(txt string, size float32, c color.NRGBA, bold bool) material.LabelStyle {
	l := material.Label(u.th, sp(size), txt)
	l.Color = c
	l.Font = u.font(bold, false, false)
	l.Shaper = u.shaper
	return l
}

func fillRect(gtx C, c color.NRGBA, sz image.Point, r int) {
	defer clip.UniformRRect(image.Rectangle{Max: sz}, r).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

// bgBox draws w on a rounded background.
func bgBox(gtx C, c color.NRGBA, radius unit.Dp, w layout.Widget) D {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	call := m.Stop()
	fillRect(gtx, c, d.Size, gtx.Dp(radius))
	call.Add(gtx.Ops)
	return d
}

// borderBox draws w with a rounded border.
func borderBox(gtx C, c color.NRGBA, radius, width unit.Dp, w layout.Widget) D {
	return widget.Border{Color: c, CornerRadius: radius, Width: width}.Layout(gtx, w)
}

func material_(gtx C, c color.NRGBA) op.CallOp {
	m := op.Record(gtx.Ops)
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	return m.Stop()
}

// button draws a small pill button.
func (u *UI) button(gtx C, btn *widget.Clickable, txt string, bg, fg color.NRGBA, outline bool) D {
	return btn.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		b := bg
		if btn.Hovered() && !outline {
			b = mix(bg, u.p.Fg, 0.12)
		}
		in := layout.Inset{Top: 5, Bottom: 5, Left: 12, Right: 12}
		content := func(gtx C) D {
			return in.Layout(gtx, u.label(txt, u.size*0.82, fg, true).Layout)
		}
		if outline {
			c := withAlpha(fg, 0x70)
			if btn.Hovered() {
				c = fg
			}
			return borderBox(gtx, c, 6, 1, content)
		}
		return bgBox(gtx, b, 6, content)
	})
}

// hoverRow wraps w in a clickable with hover highlight.
func (u *UI) hoverRow(gtx C, btn *widget.Clickable, selected bool, w layout.Widget) D {
	return btn.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		m := op.Record(gtx.Ops)
		d := w(gtx)
		call := m.Stop()
		if selected {
			fillRect(gtx, u.p.Sel, d.Size, gtx.Dp(6))
		} else if btn.Hovered() {
			fillRect(gtx, withAlpha(u.p.Sel, 0x90), d.Size, gtx.Dp(6))
		}
		call.Add(gtx.Ops)
		return d
	})
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return t.Local().Format("Jan 2")
}

// ---------------------------------------------------------------------------
// Frame

func (u *UI) activeAccount() *Account {
	app := u.app
	if app.active != nil {
		return app.active.Acct
	}
	if app.draftAcct != nil {
		return app.draftAcct
	}
	return app.accounts[0]
}

func (u *UI) Layout(gtx C) D {
	app := u.app
	a := u.activeAccount()
	u.p = a.Pal
	u.th.Palette = material.Palette{Bg: u.p.Bg, Fg: u.p.Fg, ContrastBg: u.p.Accent, ContrastFg: u.p.OnAcc}
	u.th.TextSize = sp(u.size)

	paint.Fill(gtx.Ops, u.p.Bg)
	defer u.layoutDropOverlay(gtx)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// account color strip across the top
		layout.Rigid(func(gtx C) D {
			sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(4))
			fillRect(gtx, u.p.Accent, sz, 0)
			return D{Size: sz}
		}),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(u.layoutSidebar),
				layout.Flexed(1, func(gtx C) D {
					if u.pickerAcct != nil {
						return u.layoutPicker(gtx)
					}
					s := app.active
					if s == nil && app.draftAcct != nil {
						s = u.draftFor(app.draftAcct)
					}
					if s == nil {
						return u.layoutWelcome(gtx)
					}
					return u.layoutSession(gtx, s)
				}),
			)
		}),
	)
}

func (u *UI) draftFor(a *Account) *Session {
	if u.draft == nil || u.draft.Acct != a || u.draft.ID != "" {
		u.draft = newSession(a, "", expandHome(a.Cfg.DefaultDir), "New session")
		u.draft.list.Axis = layout.Vertical
		for _, o := range a.LastCfg {
			o.Options = append([]ConfigChoice(nil), o.Options...)
			u.draft.Config = append(u.draft.Config, o)
		}
		u.draft.Commands = a.Commands
		u.dirEd.SetText(shortenHome(u.draft.CWD))
	}
	return u.draft
}

// ---------------------------------------------------------------------------
// Sidebar

func (u *UI) layoutSidebar(gtx C) D {
	app := u.app
	w := gtx.Dp(330)
	gtx.Constraints = layout.Exact(image.Pt(w, gtx.Constraints.Max.Y))
	fillRect(gtx, u.p.Side, gtx.Constraints.Max, 0)

	if u.refreshBtn.Clicked(gtx) {
		for _, a := range app.accounts {
			go app.refreshSessions(a)
		}
	}
	if u.filterAll.Clicked(gtx) {
		u.filterIdx = -1
	}
	for _, a := range app.accounts {
		if a.newBtn.Clicked(gtx) {
			app.active = nil
			app.draftAcct = a
			u.pickerAcct = nil
			u.draft = nil
			gtx.Execute(key.FocusCmd{Tag: &u.editor})
		}
		if a.filter.Clicked(gtx) {
			if u.filterIdx == a.Idx {
				u.filterIdx = -1
			} else {
				u.filterIdx = a.Idx
			}
		}
	}

	rows := app.allSessions(u.filterIdx, u.search.Text())
	for _, r := range rows {
		if r.click.Clicked(gtx) {
			u.pickerAcct = nil
			app.openSession(r)
		}
	}

	return layout.UniformInset(10).Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// New-session buttons, one per account, in the account's own color
			layout.Rigid(func(gtx C) D {
				var kids []layout.FlexChild
				for i, a := range app.accounts {
					a := a
					if i > 0 {
						kids = append(kids, layout.Rigid(layout.Spacer{Width: 8}.Layout))
					}
					kids = append(kids, layout.Flexed(1, func(gtx C) D {
						return a.newBtn.Layout(gtx, func(gtx C) D {
							pointer.CursorPointer.Add(gtx.Ops)
							bg := a.Pal.Accent
							if a.newBtn.Hovered() {
								bg = mix(bg, a.Pal.OnAcc, 0.15)
							}
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return bgBox(gtx, bg, 8, func(gtx C) D {
								return layout.Inset{Top: 8, Bottom: 8}.Layout(gtx, func(gtx C) D {
									return layout.Center.Layout(gtx, u.label("+ "+a.Cfg.Name, u.size*0.9, a.Pal.OnAcc, true).Layout)
								})
							})
						})
					}))
				}
				return layout.Flex{}.Layout(gtx, kids...)
			}),
			layout.Rigid(layout.Spacer{Height: 10}.Layout),
			// account filter chips
			layout.Rigid(func(gtx C) D {
				chip := func(btn *widget.Clickable, txt string, on bool, acc color.NRGBA) layout.FlexChild {
					return layout.Rigid(func(gtx C) D {
						return layout.Inset{Right: 6}.Layout(gtx, func(gtx C) D {
							return btn.Layout(gtx, func(gtx C) D {
								pointer.CursorPointer.Add(gtx.Ops)
								fg := u.p.Dim
								in := func(gtx C) D {
									return layout.Inset{Top: 3, Bottom: 3, Left: 10, Right: 10}.Layout(gtx, func(gtx C) D {
										return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
											layout.Rigid(func(gtx C) D {
												if acc.A == 0 {
													return D{}
												}
												s := gtx.Dp(8)
												fillRect(gtx, acc, image.Pt(s, s), s/2)
												return D{Size: image.Pt(s+gtx.Dp(6), s)}
											}),
											layout.Rigid(u.label(txt, u.size*0.78, fg, on).Layout),
										)
									})
								}
								if on {
									fg = u.p.Fg
									return bgBox(gtx, u.p.Sel, 12, in)
								}
								return borderBox(gtx, withAlpha(u.p.Dim, 0x50), 12, 1, in)
							})
						})
					})
				}
				kids := []layout.FlexChild{chip(&u.filterAll, "All", u.filterIdx == -1, color.NRGBA{})}
				for _, a := range app.accounts {
					kids = append(kids, chip(&a.filter, a.Cfg.Name, u.filterIdx == a.Idx, a.Pal.Accent))
				}
				kids = append(kids, layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }))
				kids = append(kids, layout.Rigid(func(gtx C) D {
					return u.button(gtx, &u.refreshBtn, "↻", u.p.Side, u.p.Dim, true)
				}))
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
			}),
			layout.Rigid(layout.Spacer{Height: 8}.Layout),
			// search
			layout.Rigid(func(gtx C) D {
				return bgBox(gtx, u.p.Deep, 6, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10}.Layout(gtx, func(gtx C) D {
						e := material.Editor(u.th, &u.search, "Search sessions")
						e.TextSize = sp(u.size * 0.85)
						e.Color = u.p.Fg
						e.HintColor = u.p.Dim
						e.SelectionColor = withAlpha(u.p.Accent, 0x60)
						return e.Layout(gtx)
					})
				})
			}),
			layout.Rigid(layout.Spacer{Height: 8}.Layout),
			// session list
			layout.Flexed(1, func(gtx C) D {
				l := material.List(u.th, &u.sideList)
				l.AnchorStrategy = material.Overlay
				return l.Layout(gtx, len(rows), func(gtx C, i int) D {
					return u.sessionRow(gtx, rows[i])
				})
			}),
			// connector status
			layout.Rigid(func(gtx C) D {
				var parts []string
				for _, a := range app.accounts {
					switch {
					case a.Status == "starting":
						parts = append(parts, a.Cfg.Name+": connecting…")
					case a.Status != "":
						parts = append(parts, a.Cfg.Name+": "+a.Status)
					}
				}
				if len(parts) == 0 {
					n := 0
					for _, a := range app.accounts {
						n += len(a.Sessions)
					}
					parts = append(parts, fmt.Sprintf("%d sessions", n))
				}
				return layout.Inset{Top: 6}.Layout(gtx, u.label(strings.Join(parts, " · "), u.size*0.72, u.p.Dim, false).Layout)
			}),
		)
	})
}

func (u *UI) sessionRow(gtx C, r *SessionInfo) D {
	app := u.app
	selected := app.active != nil && app.active.Acct == r.Acct && app.active.ID == r.ID
	return layout.Inset{Bottom: 2}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return u.hoverRow(gtx, &r.click, selected, func(gtx C) D {
			return layout.Flex{}.Layout(gtx,
				// account stripe
				layout.Rigid(func(gtx C) D {
					m := op.Record(gtx.Ops)
					d := layout.Spacer{Width: 4}.Layout(gtx)
					m.Stop()
					return d
				}),
				layout.Flexed(1, func(gtx C) D {
					m := op.Record(gtx.Ops)
					d := layout.Inset{Top: 7, Bottom: 7, Left: 12, Right: 8}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								l := u.label(r.Title, u.size*0.88, u.p.Fg, selected)
								l.MaxLines = 1
								return l.Layout(gtx)
							}),
							layout.Rigid(func(gtx C) D {
								l := u.label(fmt.Sprintf("%s · %s · %s", r.Acct.Cfg.Name, shortenHome(r.CWD), relTime(r.Updated)), u.size*0.7, u.p.Dim, false)
								l.MaxLines = 1
								return l.Layout(gtx)
							}),
						)
					})
					call := m.Stop()
					// stripe in the session's account color, full row height
					fillRect(gtx, r.Acct.Pal.Accent, image.Pt(gtx.Dp(4), d.Size.Y), gtx.Dp(2))
					call.Add(gtx.Ops)
					return d
				}),
			)
		})
	})
}

// ---------------------------------------------------------------------------
// Welcome

func (u *UI) layoutWelcome(gtx C) D {
	return layout.Center.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(u.label("Pick a session on the left, or start a new one.", u.size, u.p.Dim, false).Layout),
		)
	})
}

// ---------------------------------------------------------------------------
// Session view

func (u *UI) layoutSession(gtx C, s *Session) D {
	app := u.app
	a := s.Acct
	if a.themeBtn.Clicked(gtx) {
		u.openPicker(a)
	}
	if s != u.lastActive {
		u.lastActive = s
		gtx.Execute(key.FocusCmd{Tag: &u.editor})
	}
	_ = app
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return u.layoutHeader(gtx, s) }),
		layout.Flexed(1, func(gtx C) D { return u.layoutTranscript(gtx, s) }),
		layout.Rigid(func(gtx C) D { return u.layoutComposer(gtx, s) }),
	)
}

func (u *UI) layoutHeader(gtx C, s *Session) D {
	a := s.Acct
	return layout.Inset{Top: 10, Bottom: 10, Left: 18, Right: 14}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			// account pill
			layout.Rigid(func(gtx C) D {
				return bgBox(gtx, u.p.Accent, 6, func(gtx C) D {
					txt := strings.ToUpper(a.Cfg.Name)
					if a.Plan != "" {
						txt += "  ·  " + a.Plan
					}
					return layout.Inset{Top: 3, Bottom: 3, Left: 10, Right: 10}.Layout(gtx,
						u.label(txt, u.size*0.75, u.p.OnAcc, true).Layout)
				})
			}),
			layout.Rigid(layout.Spacer{Width: 12}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						l := u.label(s.Title, u.size*1.0, u.p.Bright, true)
						l.MaxLines = 1
						return l.Layout(gtx)
					}),
					layout.Rigid(func(gtx C) D {
						l := u.label(shortenHome(s.CWD), u.size*0.72, u.p.Dim, false)
						l.MaxLines = 1
						return l.Layout(gtx)
					}),
				)
			}),
			layout.Rigid(func(gtx C) D {
				name := a.Cfg.Theme
				if name == "current" {
					name = "current (" + currentThemeName() + ")"
				}
				return u.button(gtx, &a.themeBtn, "Theme: "+name, u.p.Bg, u.p.Dim, true)
			}),
		)
	})
}

func (u *UI) layoutTranscript(gtx C, s *Session) D {
	if s.Loading {
		return layout.Center.Layout(gtx, u.label("Loading conversation…", u.size, u.p.Dim, false).Layout)
	}
	if len(s.Items) == 0 && s.ID == "" {
		return layout.Center.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(u.label("New "+s.Acct.Cfg.Name+" session", u.size*1.2, u.p.Bright, true).Layout),
				layout.Rigid(layout.Spacer{Height: 6}.Layout),
				layout.Rigid(u.label("Choose the folder below, then type your first message.", u.size*0.85, u.p.Dim, false).Layout),
			)
		})
	}
	// debug: CCDESK_TESTCOPY=1 copies the last reply once, as the Copy link would
	if os.Getenv("CCDESK_TESTCOPY") != "" && !u.testCopied && !s.Loading {
		for i := len(s.Items) - 1; i >= 0; i-- {
			if s.Items[i].Kind == KAgent {
				s.Items[i].copyBtn.Click()
				u.testCopied = true
				break
			}
		}
	}
	// handle item interactions
	for _, it := range s.Items {
		if it.toggle.Clicked(gtx) {
			it.expanded = !it.expanded
		}
		if it.copyBtn.Clicked(gtx) {
			// HTML for rich editors (Teams, mail); Markdown source as the plain-text version
			it.mt.setSource(it.Text, it.Kind == KUser)
			gtx.Execute(richCopy(it.mt.rangeHTML(0, it.mt.total()), it.Text))
			u.app.setToast("Copied message")
		}
		for _, im := range it.Imgs {
			if im.copy.Clicked(gtx) {
				go copyImage(im.Mime, im.Data)
				u.app.setToast("Copied image")
			}
		}
		for _, o := range it.PermOpts {
			if o.btn.Clicked(gtx) && it.PermDone == "" {
				it.PermDone = o.Name
				select {
				case it.permCh <- o.ID:
				default:
				}
			}
		}
	}
	n := len(s.Items)
	extra := 0
	if s.Busy {
		extra = 1
	}
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			l := material.List(u.th, &s.list)
			l.AnchorStrategy = material.Overlay
			return l.Layout(gtx, n+extra, func(gtx C, i int) D {
				return u.centered(gtx, func(gtx C) D {
					if i >= n {
						return u.label("Claude is working…  (Esc to stop)", u.size*0.8, u.p.Dim, false).Layout(gtx)
					}
					return u.layoutItem(gtx, s.Items[i])
				})
			})
		}),
		layout.Stacked(func(gtx C) D {
			if u.app.toast == "" || time.Now().After(u.app.toastUntil) {
				return D{}
			}
			return layout.Inset{Top: 10, Left: 22}.Layout(gtx, func(gtx C) D {
				return bgBox(gtx, u.p.Accent, 6, func(gtx C) D {
					return layout.Inset{Top: 4, Bottom: 4, Left: 10, Right: 10}.Layout(gtx, u.label(u.app.toast, u.size*0.8, u.p.OnAcc, true).Layout)
				})
			})
		}),
	)
}

// ---------------------------------------------------------------------------
// Items

func (u *UI) layoutItem(gtx C, it *Item) D {
	switch it.Kind {
	case KUser:
		return u.layoutUser(gtx, it)
	case KAgent:
		return u.layoutAgent(gtx, it)
	case KThought:
		return u.layoutThought(gtx, it)
	case KTool:
		return u.layoutTool(gtx, it)
	case KPlan:
		return u.layoutPlan(gtx, it)
	case KPerm:
		return u.layoutPerm(gtx, it)
	case KError:
		return u.label(it.Text, u.size*0.85, u.p.Red, false).Layout(gtx)
	default:
		return u.label(it.Text, u.size*0.8, u.p.Dim, false).Layout(gtx)
	}
}

func (u *UI) selectable(gtx C, sel *widget.Selectable, txt string, f font.Font, size float32, c color.NRGBA) D {
	sel.SetText(txt)
	return sel.Layout(gtx, u.shaper, f, sp(size), material_(gtx, c), material_(gtx, withAlpha(u.p.Accent, 0x55)))
}

func (u *UI) layoutUser(gtx C, it *Item) D {
	return layout.Inset{Top: 10}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		m := op.Record(gtx.Ops)
		d := layout.Inset{Top: 10, Bottom: 10, Left: 16, Right: 14}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return u.layoutImages(gtx, it.Imgs) }),
				layout.Rigid(func(gtx C) D {
					if strings.TrimSpace(it.Text) == "" {
						return D{}
					}
					it.mt.setSource(strings.TrimSpace(it.Text), true)
					return u.layoutMsgText(gtx, &it.mt)
				}),
			)
		})
		call := m.Stop()
		fillRect(gtx, u.p.Card, d.Size, gtx.Dp(8))
		fillRect(gtx, u.p.Accent, image.Pt(gtx.Dp(3), d.Size.Y), gtx.Dp(1))
		call.Add(gtx.Ops)
		return d
	})
}

func (u *UI) layoutImages(gtx C, imgs []*Attachment) D {
	if len(imgs) == 0 {
		return D{}
	}
	var kids []layout.FlexChild
	for _, im := range imgs {
		im := im
		kids = append(kids, layout.Rigid(func(gtx C) D {
			return layout.Inset{Right: 8, Bottom: 8}.Layout(gtx, func(gtx C) D {
				return u.thumb(gtx, im, 320, 220)
			})
		}))
	}
	return layout.Flex{}.Layout(gtx, kids...)
}

// thumb draws an image thumbnail; clicking copies the image to the clipboard.
func (u *UI) thumb(gtx C, im *Attachment, maxW, maxH unit.Dp) D {
	if !im.Decode() {
		return u.label("[image: "+im.Mime+"]", u.size*0.8, u.p.Dim, false).Layout(gtx)
	}
	return im.copy.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		b := im.img.Bounds()
		w, h := float32(b.Dx()), float32(b.Dy())
		mw, mh := float32(gtx.Dp(maxW)), float32(gtx.Dp(maxH))
		scale := min(mw/w, mh/h, 1)
		sz := image.Pt(int(w*scale), int(h*scale))
		gtx.Constraints = layout.Exact(sz)
		d := widget.Image{Src: im.op, Fit: widget.Contain}.Layout(gtx)
		if im.copy.Hovered() {
			fillRect(gtx, withAlpha(u.p.Accent, 0x30), d.Size, 0)
			layout.SE.Layout(gtx, func(gtx C) D {
				return layout.UniformInset(4).Layout(gtx, func(gtx C) D {
					return bgBox(gtx, u.p.Accent, 4, func(gtx C) D {
						return layout.Inset{Left: 6, Right: 6, Top: 2, Bottom: 2}.Layout(gtx, u.label("Copy", u.size*0.7, u.p.OnAcc, true).Layout)
					})
				})
			})
		}
		return d
	})
}

func (u *UI) layoutAgent(gtx C, it *Item) D {
	it.mt.setSource(it.Text, false)
	return layout.Inset{Top: 6}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return u.layoutImages(gtx, it.Imgs) }),
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return u.layoutMsgText(gtx, &it.mt)
			}),
			layout.Rigid(func(gtx C) D {
				if strings.TrimSpace(it.Text) == "" {
					return D{}
				}
				return layout.Inset{Top: 4}.Layout(gtx, func(gtx C) D {
					return it.copyBtn.Layout(gtx, func(gtx C) D {
						pointer.CursorPointer.Add(gtx.Ops)
						c := withAlpha(u.p.Dim, 0x90)
						if it.copyBtn.Hovered() {
							c = u.p.Accent
						}
						return u.label("Copy", u.size*0.7, c, false).Layout(gtx)
					})
				})
			}),
		)
	})
}

func (u *UI) layoutThought(gtx C, it *Item) D {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return it.toggle.Layout(gtx, func(gtx C) D {
				pointer.CursorPointer.Add(gtx.Ops)
				arrow := "▸"
				if it.expanded {
					arrow = "▾"
				}
				l := u.label(arrow+" Thinking", u.size*0.8, u.p.Dim, false)
				l.Font.Style = font.Italic
				return l.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx C) D {
			if !it.expanded {
				return D{}
			}
			return layout.Inset{Left: 16, Top: 4}.Layout(gtx, func(gtx C) D {
				return u.selectable(gtx, &it.outSel, strings.TrimSpace(it.Text), u.font(false, true, false), u.size*0.85, u.p.Dim)
			})
		}),
	)
}

func (u *UI) toolStatus(it *Item) (string, color.NRGBA) {
	switch it.Status {
	case "completed":
		return "✓", u.p.Green
	case "failed":
		return "✗", u.p.Red
	case "in_progress":
		return "●", u.p.Yellow
	default:
		return "○", u.p.Dim
	}
}

func truncLines(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-maxLines)
}

func (u *UI) layoutTool(gtx C, it *Item) D {
	icon, ic := u.toolStatus(it)
	title := it.Title
	if title == "" {
		title = it.ToolKind
	}
	title = strings.ReplaceAll(title, "\n", " ")
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return bgBox(gtx, withAlpha(u.p.Card, 0xb0), 6, func(gtx C) D {
		return layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return it.toggle.Layout(gtx, func(gtx C) D {
						pointer.CursorPointer.Add(gtx.Ops)
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(u.label(icon+" ", u.size*0.85, ic, true).Layout),
							layout.Rigid(func(gtx C) D {
								k := it.ToolKind
								if k == "" {
									k = "tool"
								}
								return layout.Inset{Right: 8}.Layout(gtx, u.label(k, u.size*0.72, u.p.Accent, true).Layout)
							}),
							layout.Flexed(1, func(gtx C) D {
								l := u.label(title, u.size*0.85, u.p.Fg, false)
								l.Font = u.font(false, false, true)
								l.MaxLines = 1
								if it.expanded {
									l.MaxLines = 0
								}
								return l.Layout(gtx)
							}),
						)
					})
				}),
				layout.Rigid(func(gtx C) D {
					if !it.expanded {
						return D{}
					}
					return layout.Inset{Top: 8}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								if it.Input == "" || it.Input == it.Title {
									return D{}
								}
								return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D {
									return u.selectable(gtx, &it.inSel, truncLines(it.Input, 40), u.font(false, false, true), u.size*0.85, u.p.Dim)
								})
							}),
							layout.Rigid(func(gtx C) D { return u.layoutDiff(gtx, it.Diff) }),
							layout.Rigid(func(gtx C) D {
								if it.Output == "" {
									return D{}
								}
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								return bgBox(gtx, u.p.Deep, 4, func(gtx C) D {
									return layout.UniformInset(8).Layout(gtx, func(gtx C) D {
										return u.selectable(gtx, &it.outSel, truncLines(it.Output, 80), u.font(false, false, true), u.size*0.85, u.p.Fg)
									})
								})
							}),
						)
					})
				}),
			)
		})
	})
}

func (u *UI) layoutDiff(gtx C, diff string) D {
	if diff == "" {
		return D{}
	}
	lines := strings.Split(truncLines(diff, 200), "\n")
	var kids []layout.FlexChild
	for i, ln := range lines {
		c := u.p.Dim
		if i == 0 {
			c = u.p.Accent
		} else if strings.HasPrefix(ln, "+") {
			c = u.p.Green
		} else if strings.HasPrefix(ln, "-") {
			c = u.p.Red
		}
		ln := ln
		kids = append(kids, layout.Rigid(func(gtx C) D {
			l := u.label(ln, u.size*0.85, c, false)
			l.Font = u.font(false, false, true)
			return l.Layout(gtx)
		}))
	}
	return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return bgBox(gtx, u.p.Deep, 4, func(gtx C) D {
			return layout.UniformInset(8).Layout(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
			})
		})
	})
}

func (u *UI) layoutPlan(gtx C, it *Item) D {
	var kids []layout.FlexChild
	kids = append(kids, layout.Rigid(u.label("Plan", u.size*0.8, u.p.Accent, true).Layout))
	for _, e := range it.Plan {
		mark, c := "☐", u.p.Fg
		switch e.Status {
		case "completed":
			mark, c = "☑", u.p.Dim
		case "in_progress":
			mark, c = "◐", u.p.Yellow
		}
		txt := mark + "  " + e.Content
		kids = append(kids, layout.Rigid(u.label(txt, u.size*0.88, c, false).Layout))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

func (u *UI) layoutPerm(gtx C, it *Item) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return borderBox(gtx, u.p.Accent, 8, 2, func(gtx C) D {
		return layout.UniformInset(12).Layout(gtx, func(gtx C) D {
			kids := []layout.FlexChild{
				layout.Rigid(u.label("Permission needed", u.size*0.8, u.p.Accent, true).Layout),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 4, Bottom: 8}.Layout(gtx, func(gtx C) D {
						l := u.label(it.PermTitle, u.size, u.p.Bright, false)
						l.Font = u.font(false, false, true)
						return l.Layout(gtx)
					})
				}),
				layout.Rigid(func(gtx C) D {
					if it.Input == "" || it.Input == it.PermTitle {
						return D{}
					}
					return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx C) D {
						return u.selectable(gtx, &it.inSel, truncLines(it.Input, 30), u.font(false, false, true), u.size*0.85, u.p.Dim)
					})
				}),
			}
			if it.PermDone != "" {
				kids = append(kids, layout.Rigid(u.label("→ "+it.PermDone, u.size*0.85, u.p.Dim, false).Layout))
			} else {
				kids = append(kids, layout.Rigid(func(gtx C) D {
					var bs []layout.FlexChild
					for _, o := range it.PermOpts {
						o := o
						bs = append(bs, layout.Rigid(func(gtx C) D {
							return layout.Inset{Right: 8}.Layout(gtx, func(gtx C) D {
								if strings.HasPrefix(o.Kind, "allow") {
									return u.button(gtx, &o.btn, o.Name, u.p.Accent, u.p.OnAcc, false)
								}
								return u.button(gtx, &o.btn, o.Name, u.p.Bg, u.p.Fg, true)
							})
						}))
					}
					return layout.Flex{}.Layout(gtx, bs...)
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		})
	})
}

// ---------------------------------------------------------------------------
// Composer

func (u *UI) layoutComposer(gtx C, s *Session) D {
	app := u.app
	// Ctrl+V: paste an image if the clipboard holds one, otherwise let the editor paste text.
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &u.editor, Name: "V", Required: key.ModShortcut})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			if mime, data, ok := clipboardImage(); ok {
				u.pending = append(u.pending, &Attachment{Mime: mime, Data: data})
			} else {
				gtx.Execute(clipboard.ReadCmd{Tag: &u.editor})
			}
		}
	}
	// → or Tab on an empty box takes Claude's suggested next prompt
	if u.editor.Len() == 0 && s.Suggestion != "" && !s.Busy {
		for {
			ev, ok := gtx.Event(
				key.Filter{Focus: &u.editor, Name: key.NameRightArrow},
				key.Filter{Focus: &u.editor, Name: key.NameTab},
			)
			if !ok {
				break
			}
			if e, ok := ev.(key.Event); ok && e.State == key.Press {
				u.editor.SetText(s.Suggestion)
				n := len([]rune(s.Suggestion))
				u.editor.SetCaret(n, n)
			}
		}
	}
	matches := u.slashMatches(s)
	if len(matches) > 0 {
		u.slashIdx = min(u.slashIdx, len(matches)-1)
		for i := range matches {
			for len(u.slashBtns) <= i {
				u.slashBtns = append(u.slashBtns, new(widget.Clickable))
			}
			if u.slashBtns[i].Clicked(gtx) {
				u.completeSlash(matches[i])
				gtx.Execute(key.FocusCmd{Tag: &u.editor})
				matches = nil
				break
			}
		}
	}
	if len(matches) > 0 {
		for {
			ev, ok := gtx.Event(
				key.Filter{Focus: &u.editor, Name: key.NameUpArrow},
				key.Filter{Focus: &u.editor, Name: key.NameDownArrow},
				key.Filter{Focus: &u.editor, Name: key.NameTab},
				key.Filter{Focus: &u.editor, Name: key.NameReturn},
				key.Filter{Focus: &u.editor, Name: key.NameEscape},
			)
			if !ok {
				break
			}
			e, ok := ev.(key.Event)
			if !ok || e.State != key.Press {
				continue
			}
			switch e.Name {
			case key.NameUpArrow:
				u.slashIdx = (u.slashIdx + len(matches) - 1) % len(matches)
			case key.NameDownArrow:
				u.slashIdx = (u.slashIdx + 1) % len(matches)
			case key.NameTab, key.NameReturn:
				u.completeSlash(matches[u.slashIdx])
			case key.NameEscape:
				u.slashHidden = u.editor.Text()
			}
		}
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &u.editor, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			app.cancel(s)
		}
	}
	submit := false
	for {
		ev, ok := u.editor.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			submit = true
		}
	}
	if u.sendBtn.Clicked(gtx) {
		submit = true
	}
	if u.stopBtn.Clicked(gtx) {
		app.cancel(s)
	}
	if u.modeBtn.Clicked(gtx) && len(s.Modes) > 0 {
		next := 0
		for i, m := range s.Modes {
			if m.ID == s.ModeID {
				next = (i + 1) % len(s.Modes)
			}
		}
		app.setMode(s, s.Modes[next].ID)
	}
	var keep []*Attachment
	for _, p := range u.pending {
		if !p.del.Clicked(gtx) {
			keep = append(keep, p)
		}
	}
	u.pending = keep

	if submit && !s.Busy && s.Acct.Ready && (strings.TrimSpace(u.editor.Text()) != "" || len(u.pending) > 0) {
		if s.ID == "" {
			s.CWD = expandHome(strings.TrimSpace(u.dirEd.Text()))
			s.Title = "New session"
			app.active = s
			app.draftAcct = nil
			u.draft = nil
			u.lastActive = s
		}
		if len(u.pending) == 0 && app.handleLocalCommand(s, u.editor.Text()) {
			u.editor.SetText("")
		} else {
			app.sendPrompt(s, u.editor.Text(), u.pending)
			u.editor.SetText("")
			u.pending = nil
		}
	}

	return layout.Inset{Bottom: 14, Top: 4}.Layout(gtx, func(gtx C) D {
		return u.centered(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// folder field for new sessions
				layout.Rigid(func(gtx C) D {
					if s.ID != "" {
						return D{}
					}
					return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(u.label("Folder  ", u.size*0.8, u.p.Dim, true).Layout),
							layout.Flexed(1, func(gtx C) D {
								return bgBox(gtx, u.p.Deep, 6, func(gtx C) D {
									gtx.Constraints.Min.X = gtx.Constraints.Max.X
									return layout.Inset{Top: 5, Bottom: 5, Left: 10, Right: 10}.Layout(gtx, func(gtx C) D {
										e := material.Editor(u.th, &u.dirEd, "~/Work")
										e.Font = u.font(false, false, true)
										e.TextSize = sp(u.size * 0.9)
										e.Color = u.p.Fg
										e.HintColor = u.p.Dim
										e.SelectionColor = withAlpha(u.p.Accent, 0x60)
										return e.Layout(gtx)
									})
								})
							}),
						)
					})
				}),
				// pending image attachments
				layout.Rigid(func(gtx C) D {
					if len(u.pending) == 0 {
						return D{}
					}
					var kids []layout.FlexChild
					for _, p := range u.pending {
						p := p
						kids = append(kids, layout.Rigid(func(gtx C) D {
							return layout.Inset{Right: 8, Bottom: 8}.Layout(gtx, func(gtx C) D {
								return layout.Stack{Alignment: layout.NE}.Layout(gtx,
									layout.Stacked(func(gtx C) D { return u.thumb(gtx, p, 140, 100) }),
									layout.Stacked(func(gtx C) D {
										return u.button(gtx, &p.del, "✕", u.p.Red, u.p.OnAcc, false)
									}),
								)
							})
						}))
					}
					return layout.Flex{}.Layout(gtx, kids...)
				}),
				// editor
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					border := withAlpha(u.p.Accent, 0x60)
					if gtx.Focused(&u.editor) {
						border = u.p.Accent
					}
					d := widget.Border{Color: border, CornerRadius: 8, Width: 1.5}.Layout(gtx, func(gtx C) D {
						return bgBox(gtx, u.p.Card, 8, func(gtx C) D {
							return layout.Inset{Top: 10, Bottom: 10, Left: 14, Right: 14}.Layout(gtx, func(gtx C) D {
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								gtx.Constraints.Max.Y = gtx.Dp(260)
								hint := "Message Claude (" + s.Acct.Cfg.Name + ") — Enter to send, Shift+Enter for a new line, Ctrl+V pastes images"
								if !s.Acct.Ready {
									hint = "Connecting to " + s.Acct.Cfg.Name + " account…"
								} else if s.Suggestion != "" && !s.Busy {
									hint = s.Suggestion + "   →"
								}
								e := material.Editor(u.th, &u.editor, hint)
								e.TextSize = sp(u.size)
								e.Color = u.p.Bright
								e.HintColor = u.p.Dim
								e.SelectionColor = withAlpha(u.p.Accent, 0x60)
								return e.Layout(gtx)
							})
						})
					})
					u.editorH = d.Size.Y
					return d
				}),
				layout.Rigid(func(gtx C) D {
					// slash popup, anchored to the top of the editor (drawn on top of everything)
					u.layoutSlashPopup(gtx, u.slashMatches(s))
					return D{}
				}),
				// controls
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 8}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								if len(s.Config) > 0 {
									return u.layoutConfigMenus(gtx, s)
								}
								if len(s.Modes) == 0 {
									return D{}
								}
								name := s.ModeID
								for _, m := range s.Modes {
									if m.ID == s.ModeID {
										name = m.Name
									}
								}
								return u.button(gtx, &u.modeBtn, "Mode: "+name, u.p.Bg, u.p.Dim, true)
							}),
							layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
							layout.Rigid(func(gtx C) D {
								if s.Busy {
									return u.button(gtx, &u.stopBtn, "Stop", u.p.Red, u.p.OnAcc, false)
								}
								return u.button(gtx, &u.sendBtn, "Send", u.p.Accent, u.p.OnAcc, false)
							}),
						)
					})
				}),
			)
		})
	})
}

// centered lays w out in a readable column centred in the available width.
func (u *UI) centered(gtx C, w layout.Widget) D {
	maxW := gtx.Dp(1100)
	pad := gtx.Dp(22)
	avail := gtx.Constraints.Max.X
	cw := min(avail-2*pad, maxW)
	left := (avail - cw) / 2
	u.sideMargin = left
	c := gtx
	c.Constraints.Min.X = 0
	c.Constraints.Max.X = cw
	m := op.Record(gtx.Ops)
	d := layout.Inset{Top: 6, Bottom: 6}.Layout(c, w)
	call := m.Stop()
	off := op.Offset(image.Pt(left, 0)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	off.Pop()
	return D{Size: image.Pt(avail, d.Size.Y), Baseline: d.Baseline}
}

// ---------------------------------------------------------------------------
// Theme picker

func (u *UI) openPicker(a *Account) {
	u.pickerAcct = a
	u.themeNames = append([]string{"current"}, ListThemes()...)
	for _, n := range u.themeNames {
		if u.themeBtns[n] == nil {
			u.themeBtns[n] = new(widget.Clickable)
		}
		if _, ok := u.palCache[n]; !ok || n == "current" {
			u.palCache[n] = LoadPalette(n)
		}
	}
}

func (u *UI) layoutPicker(gtx C) D {
	app := u.app
	a := u.pickerAcct
	if u.pickerClose.Clicked(gtx) {
		u.pickerAcct = nil
		return D{}
	}
	for _, n := range u.themeNames {
		if u.themeBtns[n].Clicked(gtx) {
			a.Cfg.Theme = n
			a.Pal = LoadPalette(n)
			saveConfig(app.cfg)
		}
	}
	p := a.Pal
	return layout.Inset{Top: 16, Left: 22, Right: 22, Bottom: 16}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, u.label("Theme for "+a.Cfg.Name+" sessions", u.size*1.2, p.Bright, true).Layout),
					layout.Rigid(func(gtx C) D { return u.button(gtx, &u.pickerClose, "Done", p.Accent, p.OnAcc, false) }),
				)
			}),
			layout.Rigid(layout.Spacer{Height: 12}.Layout),
			layout.Flexed(1, func(gtx C) D {
				l := material.List(u.th, &u.pickerList)
				return l.Layout(gtx, len(u.themeNames), func(gtx C, i int) D {
					n := u.themeNames[i]
					tp := u.palCache[n]
					label := n
					if n == "current" {
						label = "current — follow Omarchy (" + currentThemeName() + ")"
					}
					return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return u.hoverRow(gtx, u.themeBtns[n], a.Cfg.Theme == n, func(gtx C) D {
							return layout.Inset{Top: 6, Bottom: 6, Left: 8, Right: 8}.Layout(gtx, func(gtx C) D {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx C) D { return u.swatch(gtx, tp) }),
									layout.Rigid(layout.Spacer{Width: 14}.Layout),
									layout.Rigid(u.label(label, u.size*0.95, u.p.Fg, a.Cfg.Theme == n).Layout),
								)
							})
						})
					})
				})
			}),
		)
	})
}

func (u *UI) swatch(gtx C, p Palette) D {
	w, h := gtx.Dp(150), gtx.Dp(30)
	fillRect(gtx, p.Bg, image.Pt(w, h), gtx.Dp(5))
	cols := []color.NRGBA{p.Accent, p.Fg, p.Red, p.Green, p.Yellow, p.Magenta}
	s := gtx.Dp(14)
	for i, c := range cols {
		off := op.Offset(image.Pt(gtx.Dp(8)+i*(s+gtx.Dp(8)), (h-s)/2)).Push(gtx.Ops)
		fillRect(gtx, c, image.Pt(s, s), s/2)
		off.Pop()
	}
	return D{Size: image.Pt(w, h)}
}

// ---------------------------------------------------------------------------
// Slash commands

func (u *UI) slashMatches(s *Session) []Command {
	t := u.editor.Text()
	if !strings.HasPrefix(t, "/") || strings.ContainsAny(t, " \n") || t == u.slashHidden {
		return nil
	}
	cmds := s.Commands
	if len(cmds) == 0 {
		cmds = s.Acct.Commands
	}
	cmds = append([]Command{
		{Name: "login", Description: "Log in to this account (opens a terminal; add \"console\" for API billing)"},
		{Name: "logout", Description: "Log out of this account"},
	}, cmds...)
	q := strings.ToLower(t[1:])
	var pre, sub []Command
	for _, c := range cmds {
		n := strings.ToLower(c.Name)
		if strings.HasPrefix(n, q) {
			pre = append(pre, c)
		} else if q != "" && strings.Contains(n, q) {
			sub = append(sub, c)
		}
	}
	out := append(pre, sub...)
	if len(out) == 1 && "/"+out[0].Name == t {
		return nil
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func (u *UI) completeSlash(c Command) {
	t := "/" + c.Name + " "
	u.editor.SetText(t)
	n := len([]rune(t))
	u.editor.SetCaret(n, n)
	u.slashIdx = 0
}

// floatAbove records w and draws it on top of everything, with its bottom edge
// `gap` above the current origin.
func floatAbove(gtx C, gap int, w layout.Widget) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	call := m.Stop()
	m2 := op.Record(gtx.Ops)
	off := op.Offset(image.Pt(0, -d.Size.Y-gap)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	off.Pop()
	op.Defer(gtx.Ops, m2.Stop())
}

func (u *UI) popupBox(gtx C, w layout.Widget) D {
	return bgBox(gtx, u.p.Side, 8, func(gtx C) D {
		return widget.Border{Color: withAlpha(u.p.Accent, 0x90), CornerRadius: 8, Width: 1}.Layout(gtx, func(gtx C) D {
			return layout.UniformInset(6).Layout(gtx, w)
		})
	})
}

// layoutSlashPopup is laid out right below the editor box and floats above it.
func (u *UI) layoutSlashPopup(gtx C, matches []Command) {
	if len(matches) == 0 {
		return
	}
	for len(u.slashBtns) < len(matches) {
		u.slashBtns = append(u.slashBtns, new(widget.Clickable))
	}
	gtx.Constraints.Min = image.Point{}
	floatAbove(gtx, u.editorH+gtx.Dp(6), func(gtx C) D {
		return u.popupBox(gtx, func(gtx C) D {
			var kids []layout.FlexChild
			for i, c := range matches {
				i, c := i, c
				kids = append(kids, layout.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return u.hoverRow(gtx, u.slashBtns[i], i == u.slashIdx, func(gtx C) D {
						return layout.Inset{Top: 4, Bottom: 4, Left: 8, Right: 8}.Layout(gtx, func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx C) D {
									gtx.Constraints.Min.X = gtx.Dp(220)
									l := u.label("/"+c.Name, u.size*0.9, u.p.Accent, true)
									l.Font = u.font(true, false, true)
									return l.Layout(gtx)
								}),
								layout.Flexed(1, func(gtx C) D {
									l := u.label(c.Description, u.size*0.8, u.p.Dim, false)
									l.MaxLines = 1
									return l.Layout(gtx)
								}),
							)
						})
					})
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		})
	})
}

// ---------------------------------------------------------------------------
// Config dropdowns (model, mode, ...)

func (u *UI) layoutConfigMenus(gtx C, s *Session) D {
	var kids []layout.FlexChild
	for i := range s.Config {
		o := &s.Config[i]
		if o.Type != "select" || len(o.Options) == 0 || u.optionHidden(o) {
			continue
		}
		kids = append(kids, layout.Rigid(func(gtx C) D {
			return layout.Inset{Right: 8}.Layout(gtx, func(gtx C) D { return u.dropdown(gtx, s, o) })
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
}

func (u *UI) optionHidden(o *ConfigOption) bool {
	for _, h := range u.app.cfg.HiddenOptions {
		if strings.EqualFold(h, o.ID) || strings.EqualFold(h, o.Name) {
			return true
		}
	}
	return false
}

func (u *UI) dropdown(gtx C, s *Session, o *ConfigOption) D {
	btn := u.ddBtns[o.ID]
	if btn == nil {
		btn = new(widget.Clickable)
		u.ddBtns[o.ID] = btn
	}
	if btn.Clicked(gtx) {
		if u.ddOpen == o.ID {
			u.ddOpen = ""
		} else {
			u.ddOpen = o.ID
		}
	}
	for _, c := range o.Options {
		if ib := u.ddItems[o.ID+"\x00"+c.Value]; ib != nil && ib.Clicked(gtx) {
			u.app.setConfig(s, o.ID, c.Value)
			u.ddOpen = ""
		}
	}
	d := u.button(gtx, btn, o.Name+": "+o.currentName()+"  ▾", u.p.Bg, u.p.Dim, true)
	if u.ddOpen != o.ID {
		return d
	}
	g2 := gtx
	g2.Constraints.Min = image.Point{}
	g2.Constraints.Max.X = gtx.Dp(460)
	floatAbove(g2, gtx.Dp(6), func(gtx C) D {
		return u.popupBox(gtx, func(gtx C) D {
			var kids []layout.FlexChild
			for _, c := range o.Options {
				c := c
				k := o.ID + "\x00" + c.Value
				if u.ddItems[k] == nil {
					u.ddItems[k] = new(widget.Clickable)
				}
				kids = append(kids, layout.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					sel := c.Value == o.CurrentValue
					return u.hoverRow(gtx, u.ddItems[k], sel, func(gtx C) D {
						return layout.Inset{Top: 5, Bottom: 5, Left: 10, Right: 10}.Layout(gtx, func(gtx C) D {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(u.label(c.Name, u.size*0.88, u.p.Fg, sel).Layout),
								layout.Rigid(func(gtx C) D {
									if c.Description == "" {
										return D{}
									}
									return u.label(c.Description, u.size*0.72, u.p.Dim, false).Layout(gtx)
								}),
							)
						})
					})
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		})
	})
	return d
}

// ---------------------------------------------------------------------------
// Drag and drop

func (u *UI) layoutDropOverlay(gtx C) {
	if !u.dragOver {
		return
	}
	fillRect(gtx, withAlpha(u.p.Accent, 0x30), gtx.Constraints.Max, 0)
	layout.Center.Layout(gtx, func(gtx C) D {
		return bgBox(gtx, u.p.Accent, 10, func(gtx C) D {
			return layout.Inset{Top: 14, Bottom: 14, Left: 24, Right: 24}.Layout(gtx,
				u.label("Drop to attach", u.size*1.2, u.p.OnAcc, true).Layout)
		})
	})
}

// handleDrop is called (under the app lock) with data dropped from another app.
func (u *UI) handleDrop(mime string, data []byte) {
	app := u.app
	if app.active == nil && app.draftAcct == nil {
		app.draftAcct = u.activeAccount()
	}
	if strings.HasPrefix(mime, "image/") {
		u.pending = append(u.pending, &Attachment{Mime: mime, Data: data})
		return
	}
	if mime != "text/uri-list" {
		u.insertText(string(data))
		return
	}
	var texts []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pu, err := url.Parse(line)
		if err != nil || pu.Scheme != "file" {
			texts = append(texts, line)
			continue
		}
		p := pu.Path
		if m, ok := imageExts[strings.ToLower(filepath.Ext(p))]; ok {
			if b, err := os.ReadFile(p); err == nil {
				u.pending = append(u.pending, &Attachment{Mime: m, Data: b})
				continue
			}
		}
		if strings.ContainsAny(p, " \t\"'") {
			p = "\"" + p + "\""
		}
		texts = append(texts, p)
	}
	if len(texts) > 0 {
		u.insertText(strings.Join(texts, " "))
	}
}

func (u *UI) insertText(t string) {
	cur := u.editor.Text()
	if cur != "" && !strings.HasSuffix(cur, " ") && !strings.HasSuffix(cur, "\n") {
		t = " " + t
	}
	u.editor.Insert(t)
}
