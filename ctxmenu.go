package main

// Right-click menu on message text: Copy and Reply. Reply quotes the selection
// into the message box as Markdown ("> ..."), the usual way to reply to part
// of a message.

import (
	"image"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"
)

type textMenu struct {
	copy, reply widget.Clickable
	catcher     int // tag for the click-outside catcher
}

func (u *UI) openTextMenu(gtx C, m *msgText, pos image.Point) {
	if u.selOwner != nil && u.selOwner != m {
		u.selOwner.clear()
	}
	u.selOwner = m
	idx := m.hit(pos)
	s, e := m.sel()
	if !m.hasSel() || idx < s || idx > e {
		// nothing selected under the pointer: use the paragraph that was clicked
		m.anchor, m.caret = m.blockAt(idx)
	}
	u.menuOwner = m
	u.menuPos = pos
	gtx.Execute(key.FocusCmd{Tag: m})
}

func (u *UI) layoutTextMenu(gtx C, m *msgText) {
	if u.menu.copy.Clicked(gtx) {
		if m.hasSel() {
			s, e := m.sel()
			gtx.Execute(richCopy(m.rangeHTML(s, e), m.selectedText()))
			u.app.setToast("Copied")
		}
		u.menuOwner = nil
		return
	}
	if u.menu.reply.Clicked(gtx) {
		u.replyWith(gtx, m.selectedText())
		m.clear()
		u.menuOwner = nil
		return
	}
	// a press anywhere else closes the menu
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &u.menu.catcher, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			u.menuOwner = nil
			return
		}
	}

	// catcher: a huge transparent area under the menu
	mc := op.Record(gtx.Ops)
	big := clip.Rect{Min: image.Pt(-20000, -20000), Max: image.Pt(20000, 20000)}.Push(gtx.Ops)
	event.Op(gtx.Ops, &u.menu.catcher)
	big.Pop()
	op.Defer(gtx.Ops, mc.Stop())

	mm := op.Record(gtx.Ops)
	off := op.Offset(u.menuPos.Add(image.Pt(2, 2))).Push(gtx.Ops)
	g := gtx
	g.Constraints.Min = image.Point{}
	g.Constraints.Max.X = gtx.Dp(220)
	u.popupBox(g, func(gtx C) D {
		item := func(btn *widget.Clickable, label, hint string) layout.FlexChild {
			return layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Dp(180)
				return u.hoverRow(gtx, btn, false, func(gtx C) D {
					return layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, u.label(label, u.size*0.88, u.p.Fg, false).Layout),
							layout.Rigid(u.label(hint, u.size*0.72, u.p.Dim, false).Layout),
						)
					})
				})
			})
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			item(&u.menu.copy, "Copy", "Ctrl+C"),
			item(&u.menu.reply, "Reply", "quote"),
		)
	})
	off.Pop()
	op.Defer(gtx.Ops, mm.Stop())
}

// replyWith quotes text into the message box and puts the caret below it.
func (u *UI) replyWith(gtx C, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	quote := strings.Join(lines, "\n") + "\n\n"
	cur := strings.TrimRight(u.editor.Text(), "\n ")
	if cur != "" {
		quote = cur + "\n\n" + quote
	}
	u.editor.SetText(quote)
	n := len([]rune(quote))
	u.editor.SetCaret(n, n)
	gtx.Execute(key.FocusCmd{Tag: &u.editor})
}
