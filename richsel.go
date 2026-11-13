package main

// Selectable rich text: a message is a stack of blocks (paragraphs, headings,
// list items, code...), each made of styled spans. We do our own word wrapping
// so we know where every rune is, which makes mouse selection and copying work
// across styles and across blocks.

import (
	"image"
	"image/color"
	"strings"
	"time"
	"unicode"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"golang.org/x/image/math/fixed"
)

// ---------------------------------------------------------------------------
// Layout of one block

type rstyle struct {
	Font  font.Font
	Size  float32 // sp
	Color color.NRGBA
}

type rtoken struct {
	span      int
	runeStart int
	nRunes    int
	newline   bool
	glyphs    []text.Glyph
	runeX     []int // x offset of each rune boundary, len nRunes+1
	w         int   // width including trailing spaces
	wText     int   // width without trailing spaces
	asc, desc int
	sizePx    int
	x, line   int
}

type rline struct {
	top, h, base int
	t0, t1       int // token range
}

type rlayout struct {
	toks  []rtoken
	lines []rline
	size  image.Point
}

func fixedCeil(v fixed.Int26_6) int { return v.Ceil() }

func (u *UI) shapeToken(gtx C, st rstyle, s string) ([]text.Glyph, []int, int) {
	u.shaper.LayoutString(text.Parameters{
		Font:     st.Font,
		PxPerEm:  fixed.I(gtx.Sp(unit.Sp(st.Size))),
		MaxWidth: 1 << 24,
		Locale:   gtx.Locale,
	}, s)
	var gs []text.Glyph
	for g, ok := u.shaper.NextGlyph(); ok; g, ok = u.shaper.NextGlyph() {
		gs = append(gs, g)
	}
	n := len([]rune(s))
	runeX := make([]int, 1, n+1)
	if len(gs) == 0 {
		for len(runeX) < n+1 {
			runeX = append(runeX, 0)
		}
		return gs, runeX, 0
	}
	x0 := gs[0].X
	clusterStart := fixed.Int26_6(0)
	inCluster := false
	end := fixed.Int26_6(0)
	for _, g := range gs {
		gx := g.X - x0
		if !inCluster {
			clusterStart = gx
			inCluster = true
		}
		if gx+g.Advance > end {
			end = gx + g.Advance
		}
		if g.Flags&text.FlagClusterBreak != 0 {
			cEnd := gx + g.Advance
			r := int(g.Runes)
			for k := 1; k <= r; k++ {
				runeX = append(runeX, (clusterStart + (cEnd-clusterStart)*fixed.Int26_6(k)/fixed.Int26_6(r)).Round())
			}
			inCluster = false
		}
	}
	for len(runeX) < n+1 {
		runeX = append(runeX, end.Round())
	}
	runeX = runeX[:n+1]
	return gs, runeX, end.Round()
}

// buildLayout shapes and wraps spans to maxW.
func (u *UI) buildLayout(gtx C, spans []string, styles []rstyle, maxW int, lineScale float32) rlayout {
	var L rlayout
	runeBase := 0
	for si, s := range spans {
		st := styles[si]
		sizePx := gtx.Sp(unit.Sp(st.Size))
		rs := []rune(s)
		i := 0
		for i < len(rs) {
			if rs[i] == '\n' {
				L.toks = append(L.toks, rtoken{span: si, runeStart: runeBase + i, nRunes: 1, newline: true, sizePx: sizePx, runeX: []int{0, 0}})
				i++
				continue
			}
			j := i
			for j < len(rs) && rs[j] != '\n' && rs[j] != ' ' {
				j++
			}
			k := j
			for k < len(rs) && rs[k] == ' ' {
				k++
			}
			L.toks = append(L.toks, u.makeToken(gtx, st, si, runeBase+i, string(rs[i:k]), k-j, sizePx)...)
			i = k
		}
		runeBase += len(rs)
	}
	// split tokens wider than the line
	var toks []rtoken
	for _, t := range L.toks {
		if t.newline || t.wText <= maxW || t.nRunes <= 1 {
			toks = append(toks, t)
			continue
		}
		toks = append(toks, u.splitToken(gtx, spans, styles, t, maxW)...)
	}
	L.toks = toks

	// wrap
	x, line := 0, 0
	lineStart := 0
	finish := func(end int) {
		ln := rline{t0: lineStart, t1: end}
		asc, desc, h := 0, 0, 0
		for _, t := range L.toks[lineStart:end] {
			if !t.newline || end-lineStart == 1 {
				asc = max(asc, t.asc)
				desc = max(desc, t.desc)
			}
			h = max(h, int(float32(t.sizePx)*lineScale+0.5))
		}
		if asc == 0 && desc == 0 && end > lineStart {
			t := L.toks[lineStart]
			asc, desc = t.sizePx*4/5, t.sizePx/4
		}
		h = max(h, asc+desc)
		top := 0
		if len(L.lines) > 0 {
			p := L.lines[len(L.lines)-1]
			top = p.top + p.h
		}
		ln.top, ln.h = top, h
		ln.base = top + (h-(asc+desc))/2 + asc
		L.lines = append(L.lines, ln)
		lineStart = end
	}
	for i := range L.toks {
		t := &L.toks[i]
		if !t.newline && x > 0 && x+t.wText > maxW {
			finish(i)
			line++
			x = 0
		}
		t.x, t.line = x, line
		if t.newline {
			finish(i + 1)
			line++
			x = 0
			continue
		}
		x += t.w
	}
	if lineStart < len(L.toks) || len(L.lines) == 0 {
		if len(L.toks) == 0 {
			L.lines = append(L.lines, rline{})
		} else {
			finish(len(L.toks))
		}
	}
	w := 0
	for _, t := range L.toks {
		w = max(w, t.x+t.wText)
	}
	last := L.lines[len(L.lines)-1]
	L.size = image.Pt(min(w, maxW), last.top+last.h)
	return L
}

func (u *UI) makeToken(gtx C, st rstyle, si, runeStart int, s string, trailing, sizePx int) []rtoken {
	gs, runeX, w := u.shapeToken(gtx, st, s)
	n := len([]rune(s))
	t := rtoken{span: si, runeStart: runeStart, nRunes: n, glyphs: gs, runeX: runeX, w: w, sizePx: sizePx}
	t.wText = runeX[n-trailing]
	for _, g := range gs {
		t.asc = max(t.asc, fixedCeil(g.Ascent))
		t.desc = max(t.desc, fixedCeil(g.Descent))
	}
	return []rtoken{t}
}

func (u *UI) splitToken(gtx C, spans []string, styles []rstyle, t rtoken, maxW int) []rtoken {
	// rune offset of this token within its span
	spanStart := 0
	for i := 0; i < t.span; i++ {
		spanStart += len([]rune(spans[i]))
	}
	rs := []rune(spans[t.span])[t.runeStart-spanStart : t.runeStart-spanStart+t.nRunes]
	var out []rtoken
	start := 0
	for start < len(rs) {
		// find the longest prefix that fits
		_, rx, _ := u.shapeToken(gtx, styles[t.span], string(rs[start:]))
		k := 1
		for k < len(rs)-start && rx[k+1] <= maxW {
			k++
		}
		piece := string(rs[start : start+k])
		trailing := len(piece) - len(strings.TrimRight(piece, " "))
		out = append(out, u.makeToken(gtx, styles[t.span], t.span, t.runeStart+start, piece, trailing, t.sizePx)...)
		start += k
	}
	return out
}

func (L *rlayout) hit(p image.Point) int {
	if len(L.toks) == 0 {
		return 0
	}
	li := len(L.lines) - 1
	for i, ln := range L.lines {
		if p.Y < ln.top+ln.h {
			li = i
			break
		}
	}
	ln := L.lines[li]
	if ln.t0 >= ln.t1 {
		if ln.t0 < len(L.toks) {
			return L.toks[ln.t0].runeStart
		}
		return L.end()
	}
	if p.X <= L.toks[ln.t0].x {
		return L.toks[ln.t0].runeStart
	}
	for i := ln.t0; i < ln.t1; i++ {
		t := L.toks[i]
		if t.newline {
			return t.runeStart
		}
		if p.X < t.x+t.w || i == ln.t1-1 {
			best, bd := 0, 1<<30
			for k, rx := range t.runeX {
				d := p.X - (t.x + rx)
				if d < 0 {
					d = -d
				}
				if d < bd {
					best, bd = k, d
				}
			}
			idx := t.runeStart + best
			// at a soft wrap, don't land after the trailing space on the next line
			if i == ln.t1-1 && li < len(L.lines)-1 && best == t.nRunes && t.nRunes > 0 {
				idx--
			}
			return idx
		}
	}
	return L.end()
}

func (L *rlayout) end() int {
	if len(L.toks) == 0 {
		return 0
	}
	t := L.toks[len(L.toks)-1]
	return t.runeStart + t.nRunes
}

// paint draws the layout with an optional selection [s,e) (block-local runes).
func (u *UI) paintLayout(gtx C, L *rlayout, styles []rstyle, s, e int, selColor color.NRGBA) {
	if s < e {
		for _, ln := range L.lines {
			for i := ln.t0; i < ln.t1; i++ {
				t := L.toks[i]
				a, b := max(s, t.runeStart), min(e, t.runeStart+t.nRunes)
				if a >= b {
					continue
				}
				x0 := t.x + t.runeX[a-t.runeStart]
				x1 := t.x + t.runeX[b-t.runeStart]
				if t.newline {
					x1 = x0 + gtx.Dp(6)
				}
				r := image.Rect(x0, ln.top, x1, ln.top+ln.h)
				st := clip.Rect(r).Push(gtx.Ops)
				paint.Fill(gtx.Ops, selColor)
				st.Pop()
			}
		}
	}
	for _, ln := range L.lines {
		for i := ln.t0; i < ln.t1; i++ {
			t := L.toks[i]
			if t.newline || len(t.glyphs) == 0 {
				continue
			}
			x0 := t.glyphs[0].X
			off := op.Affine(f32.AffineId().Offset(f32.Pt(float32(t.x)+float32(x0)/64, float32(ln.base)))).Push(gtx.Ops)
			path := u.shaper.Shape(t.glyphs)
			outline := clip.Outline{Path: path}.Op().Push(gtx.Ops)
			paint.ColorOp{Color: styles[t.span].Color}.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			outline.Pop()
			if call := u.shaper.Bitmaps(t.glyphs); call != (op.CallOp{}) {
				call.Add(gtx.Ops)
			}
			off.Pop()
		}
	}
}

// ---------------------------------------------------------------------------
// Message text: blocks + selection

type rspan struct {
	Text               string
	Bold, Italic, Code bool
}

type rblock struct {
	Kind   mdKind
	Level  int
	Marker string
	Spans  []rspan
	plain  []rune
	sig    string

	lay    rlayout
	layKey [3]int
	org    image.Point // text origin in message coordinates (last frame)
	size   image.Point // block size
	base   int         // global rune offset
}

type msgText struct {
	src    string
	plain  bool // raw text, no markdown
	blocks []*rblock

	anchor, caret int
	dragging      bool
	lastPress     time.Time
	clicks        int
}

func newRBlock(kind mdKind, level int, marker string, spans []rspan) *rblock {
	b := &rblock{Kind: kind, Level: level, Marker: marker, Spans: spans}
	var sb strings.Builder
	for _, s := range spans {
		sb.WriteString(s.Text)
	}
	b.plain = []rune(sb.String())
	b.sig = string(rune('a'+int(kind))) + marker + "|" + sb.String()
	for _, s := range spans {
		if s.Bold {
			b.sig += "B"
		}
		if s.Italic {
			b.sig += "I"
		}
		if s.Code {
			b.sig += "C"
		}
	}
	return b
}

// setSource rebuilds blocks from markdown (or raw text), reusing unchanged blocks' layouts.
func (m *msgText) setSource(src string, raw bool) {
	if m.src == src && m.blocks != nil {
		return
	}
	m.src = src
	m.plain = raw
	var nb []*rblock
	if raw {
		nb = append(nb, newRBlock(mdPara, 0, "", []rspan{{Text: strings.ReplaceAll(src, "\t", "    ")}}))
	} else {
		for _, b := range parseMarkdown(src) {
			var spans []rspan
			switch b.Kind {
			case mdCode, mdTable:
				spans = []rspan{{Text: strings.ReplaceAll(b.Text, "\t", "    "), Code: true}}
			case mdRule:
			default:
				for _, s := range parseInline(b.Text) {
					spans = append(spans, rspan{Text: s.Text, Bold: s.Bold, Italic: s.Italic, Code: s.Code})
				}
			}
			nb = append(nb, newRBlock(b.Kind, b.Level, b.Marker, spans))
		}
	}
	old := map[string]*rblock{}
	for _, b := range m.blocks {
		old[b.sig] = b
	}
	for i, b := range nb {
		if o := old[b.sig]; o != nil {
			nb[i] = o
		}
	}
	m.blocks = nb
	base := 0
	for _, b := range m.blocks {
		b.base = base
		base += len(b.plain) + 1
	}
	total := max(base-1, 0)
	m.anchor = min(m.anchor, total)
	m.caret = min(m.caret, total)
}

func (m *msgText) total() int {
	if len(m.blocks) == 0 {
		return 0
	}
	b := m.blocks[len(m.blocks)-1]
	return b.base + len(b.plain)
}

func (m *msgText) sel() (int, int) {
	return min(m.anchor, m.caret), max(m.anchor, m.caret)
}

func (m *msgText) hasSel() bool { return m.anchor != m.caret }

func (m *msgText) clear() { m.anchor, m.caret = 0, 0 }

func (m *msgText) selectedText() string {
	s, e := m.sel()
	var sb strings.Builder
	var prev *rblock
	for _, b := range m.blocks {
		a, z := max(s-b.base, 0), min(e-b.base, len(b.plain))
		if a >= z {
			continue
		}
		if prev != nil {
			if prev.Kind == mdBullet && b.Kind == mdBullet {
				sb.WriteString("\n")
			} else {
				sb.WriteString("\n\n")
			}
		}
		if b.Kind == mdBullet && a == 0 {
			sb.WriteString(strings.Repeat("  ", b.Level))
			if b.Marker == "•" {
				sb.WriteString("- ")
			} else {
				sb.WriteString(b.Marker + " ")
			}
		}
		sb.WriteString(string(b.plain[a:z]))
		prev = b
	}
	return sb.String()
}

func (m *msgText) hit(p image.Point) int {
	if len(m.blocks) == 0 {
		return 0
	}
	for i, b := range m.blocks {
		bottom := b.org.Y + b.lay.size.Y
		if i+1 < len(m.blocks) {
			bottom = m.blocks[i+1].org.Y - 1
		}
		if p.Y <= bottom || i == len(m.blocks)-1 {
			if p.Y < b.org.Y-gtxSlack && i > 0 {
				return b.base
			}
			return b.base + b.lay.hit(p.Sub(b.org))
		}
	}
	return m.total()
}

const gtxSlack = 2

func (m *msgText) wordAt(idx int) (int, int) {
	for _, b := range m.blocks {
		if idx < b.base || idx > b.base+len(b.plain) {
			continue
		}
		local := idx - b.base
		isWord := func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == '/' || r == '~'
		}
		s, e := local, local
		for s > 0 && isWord(b.plain[s-1]) {
			s--
		}
		for e < len(b.plain) && isWord(b.plain[e]) {
			e++
		}
		return b.base + s, b.base + e
	}
	return idx, idx
}

func (m *msgText) blockAt(idx int) (int, int) {
	for _, b := range m.blocks {
		if idx >= b.base && idx <= b.base+len(b.plain) {
			return b.base, b.base + len(b.plain)
		}
	}
	return idx, idx
}

// ---------------------------------------------------------------------------
// UI integration

func (u *UI) blockStyles(b *rblock, raw bool) ([]string, []rstyle) {
	size := u.size
	p := u.p
	var texts []string
	var styles []rstyle
	for _, s := range b.Spans {
		st := rstyle{Font: u.font(s.Bold, s.Italic, false), Size: size, Color: p.Fg}
		switch b.Kind {
		case mdHeading:
			st.Size = size * [7]float32{1, 1.35, 1.2, 1.08, 1, 1, 1}[min(b.Level, 6)]
			st.Font.Weight = font.Bold
			st.Color = p.Bright
		case mdQuote:
			st.Color = p.Dim
		case mdCode, mdTable:
			st.Font = u.font(false, false, true)
		}
		if raw {
			st.Color = p.Bright
		}
		if s.Code && b.Kind != mdCode && b.Kind != mdTable {
			st.Font = u.font(false, false, true)
			st.Size = size * 1.06
			st.Color = p.Magenta
		}
		texts = append(texts, s.Text)
		styles = append(styles, st)
	}
	return texts, styles
}

// layoutMsgText lays out all blocks of a message with selection support.
func (u *UI) layoutMsgText(gtx C, m *msgText) D {
	// input from the previous frame's geometry
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: m, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel},
			key.FocusFilter{Target: m},
			key.Filter{Focus: m, Name: "C", Required: key.ModShortcut},
			key.Filter{Focus: m, Name: "A", Required: key.ModShortcut},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			pos := image.Pt(int(e.Position.X), int(e.Position.Y))
			switch e.Kind {
			case pointer.Press:
				if e.Buttons != pointer.ButtonPrimary {
					break
				}
				gtx.Execute(key.FocusCmd{Tag: m})
				if u.selOwner != nil && u.selOwner != m {
					u.selOwner.clear()
				}
				u.selOwner = m
				now := time.Now()
				if now.Sub(m.lastPress) < 400*time.Millisecond {
					m.clicks++
				} else {
					m.clicks = 1
				}
				m.lastPress = now
				idx := m.hit(pos)
				switch {
				case m.clicks == 2:
					m.anchor, m.caret = m.wordAt(idx)
				case m.clicks >= 3:
					m.anchor, m.caret = m.blockAt(idx)
				default:
					if e.Modifiers.Contain(key.ModShift) && u.selOwner == m {
						m.caret = idx
					} else {
						m.anchor, m.caret = idx, idx
					}
				}
				m.dragging = true
			case pointer.Drag:
				if m.dragging && m.clicks == 1 {
					m.caret = m.hit(pos)
				}
			case pointer.Release, pointer.Cancel:
				m.dragging = false
			}
		case key.Event:
			if e.State != key.Press {
				break
			}
			switch e.Name {
			case "C":
				if m.hasSel() {
					s, e := m.sel()
					gtx.Execute(richCopy(m.rangeHTML(s, e), m.selectedText()))
					u.app.setToast("Copied")
				}
			case "A":
				m.anchor, m.caret = 0, m.total()
			}
		}
	}

	maxW := gtx.Constraints.Max.X
	s, e := m.sel()
	selCol := withAlpha(u.p.Accent, 0x55)
	y := 0
	gap := gtx.Dp(8)
	for i, b := range m.blocks {
		if i > 0 {
			y += gap
		}
		if b.Kind == mdHeading && i > 0 {
			y += gtx.Dp(6)
		}
		// geometry of the text area within the block
		textOrg := image.Pt(0, 0)
		textW := maxW
		pad := 0
		switch b.Kind {
		case mdCode, mdTable:
			pad = gtx.Dp(10)
			textOrg = image.Pt(pad, pad)
			textW = maxW - 2*pad
		case mdBullet:
			ind := gtx.Dp(unit.Dp(4 + 18*b.Level))
			textOrg = image.Pt(ind+gtx.Dp(22), 0)
			textW = maxW - textOrg.X
		case mdQuote:
			textOrg = image.Pt(gtx.Dp(14), 0)
			textW = maxW - textOrg.X
		}
		textW = max(textW, gtx.Dp(40))
		texts, styles := u.blockStyles(b, m.plain)
		lk := [3]int{textW, int(gtx.Metric.PxPerSp * 1000), int(u.size * 100)}
		if b.layKey != lk {
			b.lay = u.buildLayout(gtx, texts, styles, textW, 1.3)
			b.layKey = lk
		}
		h := b.lay.size.Y
		if b.Kind == mdRule {
			h = gtx.Dp(1)
		}
		blockH := h + 2*pad
		off := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
		switch b.Kind {
		case mdCode, mdTable:
			fillRect(gtx, u.p.Deep, image.Pt(maxW, blockH), gtx.Dp(6))
		case mdQuote:
			fillRect(gtx, u.p.Muted, image.Pt(gtx.Dp(3), blockH), 0)
		case mdRule:
			fillRect(gtx, u.p.Muted, image.Pt(maxW, h), 0)
		case mdBullet:
			ind := gtx.Dp(unit.Dp(4 + 18*b.Level))
			mo := op.Offset(image.Pt(ind, 0)).Push(gtx.Ops)
			u.label(b.Marker, u.size, u.p.Accent, true).Layout(gtx)
			mo.Pop()
		}
		to := op.Offset(textOrg).Push(gtx.Ops)
		u.paintLayout(gtx, &b.lay, styles, max(s-b.base, 0), min(e-b.base, len(b.plain)), selCol)
		to.Pop()
		off.Pop()
		b.org = textOrg.Add(image.Pt(0, y))
		b.size = image.Pt(maxW, blockH)
		y += blockH
	}
	size := image.Pt(maxW, y)
	// Accept presses in the empty margins beside (and just around) the text too,
	// so a selection can start there; hits outside clamp to line starts/ends.
	ext := u.sideMargin + gtx.Dp(40)
	vext := gtx.Dp(6)
	area := clip.Rect{Min: image.Pt(-ext, -vext), Max: image.Pt(maxW+ext, y+vext)}.Push(gtx.Ops)
	event.Op(gtx.Ops, m)
	pointer.CursorText.Add(gtx.Ops)
	area.Pop()
	return D{Size: size}
}

var _ = layout.Dimensions{}
