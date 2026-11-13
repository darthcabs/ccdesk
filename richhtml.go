package main

// Rich clipboard: copies carry HTML (for Teams, mail, docs) plus plain text.

import (
	"html"
	"strings"

	"gioui.org/app"
	"gioui.org/io/clipboard"
)

// selectionHTML renders the selected range [s,e) of m as HTML.
func (m *msgText) rangeHTML(s, e int) string {
	var sb strings.Builder
	type open struct {
		tag   string
		level int
	}
	var lists []open
	closeLists := func(level int) {
		for len(lists) > 0 && lists[len(lists)-1].level >= level {
			sb.WriteString("</li></" + lists[len(lists)-1].tag + ">")
			lists = lists[:len(lists)-1]
		}
	}
	for _, b := range m.blocks {
		a, z := max(s-b.base, 0), min(e-b.base, len(b.plain))
		if a >= z && b.Kind != mdRule {
			continue
		}
		if b.Kind == mdRule && (b.base < s || b.base > e) {
			continue
		}
		inner := spansHTML(b.Spans, a, z)
		if b.Kind != mdBullet {
			closeLists(0)
		}
		switch b.Kind {
		case mdHeading:
			lvl := min(max(b.Level, 1)+1, 6) // h1 is huge in chat apps
			t := "h" + string(rune('0'+lvl))
			sb.WriteString("<" + t + ">" + inner + "</" + t + ">")
		case mdCode, mdTable:
			sb.WriteString(`<pre><code>` + html.EscapeString(string(b.plain[a:z])) + `</code></pre>`)
		case mdQuote:
			sb.WriteString("<blockquote>" + nl2br(inner) + "</blockquote>")
		case mdRule:
			sb.WriteString("<hr>")
		case mdBullet:
			tag := "ul"
			if b.Marker != "•" {
				tag = "ol"
			}
			if len(lists) > 0 && lists[len(lists)-1].level > b.Level {
				closeLists(b.Level + 1)
			}
			if len(lists) > 0 && lists[len(lists)-1].level == b.Level {
				if lists[len(lists)-1].tag != tag {
					closeLists(b.Level)
				} else {
					sb.WriteString("</li>")
				}
			}
			if len(lists) == 0 || lists[len(lists)-1].level < b.Level {
				sb.WriteString("<" + tag + ">")
				lists = append(lists, open{tag, b.Level})
			}
			sb.WriteString("<li>" + inner)
		default:
			sb.WriteString("<p>" + nl2br(inner) + "</p>")
		}
	}
	closeLists(0)
	return sb.String()
}

func nl2br(s string) string { return strings.ReplaceAll(s, "\n", "<br>") }

// spansHTML renders the runes [a,z) of a block's spans.
func spansHTML(spans []rspan, a, z int) string {
	var sb strings.Builder
	pos := 0
	for _, sp := range spans {
		r := []rune(sp.Text)
		lo, hi := max(a-pos, 0), min(z-pos, len(r))
		pos += len(r)
		if lo >= hi {
			continue
		}
		t := html.EscapeString(string(r[lo:hi]))
		switch {
		case sp.Code:
			t = "<code>" + t + "</code>"
		default:
			if sp.Italic {
				t = "<i>" + t + "</i>"
			}
			if sp.Bold {
				t = "<b>" + t + "</b>"
			}
		}
		sb.WriteString(t)
	}
	return sb.String()
}

// richCopy builds a clipboard write offering both HTML and plain text.
func richCopy(htmlBody, plain string) clipboard.WriteCmd {
	doc := "<meta charset=\"utf-8\">" + htmlBody
	return clipboard.WriteCmd{Type: app.RichClipboardMime, Data: readCloser(doc + "\x00" + plain)}
}
