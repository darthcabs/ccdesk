package main

// Right-click menu on sidebar sessions: Rename (inline) and Remove from list.

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"
)

type rowMenu struct {
	rename, remove widget.Clickable
	catcher        int
}

func (u *UI) rowMenuEvents(gtx C, r *SessionInfo) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: r, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Buttons == pointer.ButtonSecondary {
			u.rowMenu = r
			u.rowMenuPos = image.Pt(int(e.Position.X), int(e.Position.Y))
		}
	}
}

func (u *UI) startRename(gtx C, r *SessionInfo) {
	u.renaming = r
	u.renameEd.SingleLine = true
	u.renameEd.Submit = true
	u.renameEd.SetText(r.Title)
	n := len([]rune(r.Title))
	u.renameEd.SetCaret(n, 0) // select all, so typing replaces the title
	gtx.Execute(key.FocusCmd{Tag: &u.renameEd})
}

func (u *UI) layoutRowMenu(gtx C, r *SessionInfo) {
	if u.rowMenuW.rename.Clicked(gtx) {
		u.rowMenu = nil
		u.startRename(gtx, r)
		return
	}
	if u.rowMenuW.remove.Clicked(gtx) {
		u.rowMenu = nil
		u.app.removeSessionByID(r.Acct, r.ID)
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &u.rowMenuW.catcher, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			u.rowMenu = nil
			return
		}
	}
	mc := op.Record(gtx.Ops)
	big := clip.Rect{Min: image.Pt(-20000, -20000), Max: image.Pt(20000, 20000)}.Push(gtx.Ops)
	event.Op(gtx.Ops, &u.rowMenuW.catcher)
	big.Pop()
	op.Defer(gtx.Ops, mc.Stop())

	mm := op.Record(gtx.Ops)
	off := op.Offset(u.rowMenuPos.Add(image.Pt(2, 2))).Push(gtx.Ops)
	g := gtx
	g.Constraints.Min = image.Point{}
	g.Constraints.Max.X = gtx.Dp(220)
	u.popupBox(g, func(gtx C) D {
		item := func(btn *widget.Clickable, label string) layout.FlexChild {
			return layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Dp(180)
				return u.hoverRow(gtx, btn, false, func(gtx C) D {
					return layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10}.Layout(gtx,
						u.label(label, u.size*0.88, u.p.Fg, false).Layout)
				})
			})
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			item(&u.rowMenuW.rename, "Rename"),
			item(&u.rowMenuW.remove, "Remove from list"),
		)
	})
	off.Pop()
	op.Defer(gtx.Ops, mm.Stop())
}

// renameRow is the sidebar row while its title is being edited:
// Enter saves, Esc (or clicking elsewhere) cancels.
func (u *UI) renameRow(gtx C, r *SessionInfo) D {
	for {
		ev, ok := u.renameEd.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			u.app.renameSession(r, u.renameEd.Text())
			u.renaming = nil
			gtx.Execute(key.FocusCmd{Tag: &u.editor})
		}
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &u.renameEd, Name: key.NameEscape}, key.FocusFilter{Target: &u.renameEd})
		if !ok {
			break
		}
		switch e := ev.(type) {
		case key.Event:
			if e.State == key.Press {
				u.renaming = nil
			}
		case key.FocusEvent:
			if !e.Focus {
				u.renaming = nil
			}
		}
	}
	return layout.Inset{Bottom: 2}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return u.field(gtx, &u.renameEd, "Session title", u.size*0.88, false, false)
	})
}

