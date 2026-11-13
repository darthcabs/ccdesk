package main

import (
	"image"
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func testUI() (*UI, layout.Context) {
	cfg := defaultConfig()
	a := &App{cfg: cfg, sessions: map[string]*Session{}, invalidate: func() {}}
	for i := range a.cfg.Accounts {
		ac := &a.cfg.Accounts[i]
		a.accounts = append(a.accounts, &Account{Idx: i, Cfg: ac, Pal: LoadPalette("tokyo-night")})
	}
	u := newUI(a)
	u.p = a.accounts[0].Pal
	gtx := layout.Context{
		Ops:         new(op.Ops),
		Constraints: layout.Exact(image.Pt(600, 2000)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
	gtx.Constraints.Min = image.Point{}
	return u, gtx
}

const sample = "Hello **bold** and `code` world, this is a long paragraph that should wrap onto more than one line at six hundred pixels wide.\n\n- item one\n- item two\n\n```\nfmt.Println(\"hi\")\n```"

func TestSelection(t *testing.T) {
	u, gtx := testUI()
	var m msgText
	m.setSource(sample, false)
	d := u.layoutMsgText(gtx, &m)
	if len(m.blocks) != 4 {
		t.Fatalf("blocks = %d", len(m.blocks))
	}
	if len(m.blocks[0].lay.lines) < 2 {
		t.Fatalf("paragraph did not wrap: %d lines", len(m.blocks[0].lay.lines))
	}
	if d.Size.Y <= 0 {
		t.Fatal("no height")
	}
	// click at very start -> 0
	if got := m.hit(image.Pt(0, 1)); got != 0 {
		t.Errorf("hit start = %d", got)
	}
	// select from start through the middle of "item two"
	b2 := m.blocks[2]
	m.anchor = 0
	m.caret = m.hit(b2.org.Add(image.Pt(500, 5)))
	got := m.selectedText()
	want := "Hello bold and code world"
	if !strings.HasPrefix(got, want) || !strings.Contains(got, "\n\n- item one\n- item two") {
		t.Errorf("selected text = %q", got)
	}
	// double-click word
	s, e := m.wordAt(m.blocks[0].base + 8) // inside "bold"
	if w := string([]rune(m.blocks[0].plain)[s:e]); w != "bold" {
		t.Errorf("word = %q", w)
	}
	// code block selection keeps the code
	cb := m.blocks[3]
	m.anchor, m.caret = m.blockAt(cb.base + 1)
	if got := m.selectedText(); got != `fmt.Println("hi")` {
		t.Errorf("code = %q", got)
	}
	// clicking on the second line of the paragraph lands past the first line
	l1 := m.blocks[0].lay.lines[1]
	idx := m.hit(image.Pt(1, l1.top+l1.h/2))
	if idx <= 0 || idx != m.blocks[0].lay.toks[l1.t0].runeStart {
		t.Errorf("second line start hit = %d, want %d", idx, m.blocks[0].lay.toks[l1.t0].runeStart)
	}
}

func TestRichHTML(t *testing.T) {
	var m msgText
	m.setSource("Hello **bold** and `code`.\n\n- one\n- two\n  - nested\n\n1. first\n\n```\nx < y\n```", false)
	got := m.rangeHTML(0, m.total())
	for _, want := range []string{"<p>Hello <b>bold</b> and <code>code</code>.</p>", "<ul><li>one</li><li>two<ul><li>nested</li></ul></li></ul>", "<ol><li>first</li></ol>", "<pre><code>x &lt; y</code></pre>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestHitInMargins(t *testing.T) {
	u, gtx := testUI()
	var m msgText
	m.setSource(sample, false)
	u.layoutMsgText(gtx, &m)
	L := &m.blocks[0].lay
	l0, l1 := L.lines[0], L.lines[1]
	// left of the column on line 2 -> start of line 2
	if got, want := m.hit(image.Pt(-200, l1.top+l1.h/2)), L.toks[l1.t0].runeStart; got != want {
		t.Errorf("left margin hit = %d, want %d", got, want)
	}
	// right of the column on line 1 -> end of line 1 (before the wrap space)
	last := L.toks[l0.t1-1]
	if got, want := m.hit(image.Pt(5000, l0.top+l0.h/2)), last.runeStart+last.nRunes-1; got != want {
		t.Errorf("right margin hit = %d, want %d", got, want)
	}
}
