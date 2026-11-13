package main

// A small Markdown block parser: enough for Claude's replies (paragraphs, headings,
// lists, quotes, fenced code, tables, rules) with inline **bold**, *italic*, `code`.

import (
	"strings"
)

type mdKind int

const (
	mdPara mdKind = iota
	mdHeading
	mdCode
	mdBullet
	mdQuote
	mdRule
	mdTable
)

type mdBlock struct {
	Kind   mdKind
	Level  int    // heading level, or list indent depth
	Marker string // list marker ("•" or "1.")
	Text   string
}

func parseMarkdown(src string) []mdBlock {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []mdBlock
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, mdBlock{Kind: mdPara, Text: strings.Join(para, "\n")})
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			flush()
			fence := trim[:3]
			var code []string
			i++
			for ; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
					break
				}
				code = append(code, lines[i])
			}
			out = append(out, mdBlock{Kind: mdCode, Text: strings.Join(code, "\n")})
		case trim == "":
			flush()
		case strings.HasPrefix(trim, "#"):
			lvl := 0
			for lvl < len(trim) && trim[lvl] == '#' {
				lvl++
			}
			if lvl <= 6 && lvl < len(trim) && trim[lvl] == ' ' {
				flush()
				out = append(out, mdBlock{Kind: mdHeading, Level: lvl, Text: strings.TrimSpace(trim[lvl:])})
			} else {
				para = append(para, line)
			}
		case trim == "---" || trim == "***" || trim == "___":
			flush()
			out = append(out, mdBlock{Kind: mdRule})
		case strings.HasPrefix(trim, "|"):
			flush()
			var rows []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				rows = append(rows, strings.TrimSpace(lines[i]))
			}
			i--
			out = append(out, mdBlock{Kind: mdTable, Text: formatTable(rows)})
		case strings.HasPrefix(trim, ">"):
			flush()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
			}
			i--
			out = append(out, mdBlock{Kind: mdQuote, Text: strings.Join(q, "\n")})
		default:
			if marker, rest, ok := listItem(trim); ok {
				flush()
				indent := (len(line) - len(strings.TrimLeft(line, " \t"))) / 2
				text := rest
				// continuation lines (indented, not a new item)
				for i+1 < len(lines) {
					nx := lines[i+1]
					nt := strings.TrimSpace(nx)
					if nt == "" || !strings.HasPrefix(nx, "  ") {
						break
					}
					if _, _, isItem := listItem(nt); isItem || strings.HasPrefix(nt, "```") {
						break
					}
					text += " " + nt
					i++
				}
				out = append(out, mdBlock{Kind: mdBullet, Level: indent, Marker: marker, Text: text})
			} else {
				para = append(para, trim)
			}
		}
	}
	flush()
	return out
}

func listItem(trim string) (marker, rest string, ok bool) {
	if len(trim) >= 2 && (trim[0] == '-' || trim[0] == '*' || trim[0] == '+') && trim[1] == ' ' {
		return "•", strings.TrimSpace(trim[2:]), true
	}
	j := 0
	for j < len(trim) && j < 4 && trim[j] >= '0' && trim[j] <= '9' {
		j++
	}
	if j > 0 && j+1 < len(trim) && (trim[j] == '.' || trim[j] == ')') && trim[j+1] == ' ' {
		return trim[:j+1], strings.TrimSpace(trim[j+2:]), true
	}
	return "", "", false
}

func formatTable(rows []string) string {
	var cells [][]string
	for _, r := range rows {
		r = strings.Trim(r, "|")
		parts := strings.Split(r, "|")
		sep := true
		for k := range parts {
			parts[k] = strings.TrimSpace(stripInline(parts[k]))
			if strings.Trim(parts[k], ":- ") != "" {
				sep = false
			}
		}
		if sep {
			continue
		}
		cells = append(cells, parts)
	}
	var widths []int
	for _, r := range cells {
		for k, c := range r {
			for len(widths) <= k {
				widths = append(widths, 0)
			}
			if n := len([]rune(c)); n > widths[k] {
				widths[k] = n
			}
		}
	}
	var sb strings.Builder
	for ri, r := range cells {
		for k, c := range r {
			sb.WriteString(c)
			if k < len(r)-1 {
				sb.WriteString(strings.Repeat(" ", widths[k]-len([]rune(c))+2))
			}
		}
		sb.WriteByte('\n')
		if ri == 0 && len(cells) > 1 {
			total := 0
			for _, w := range widths {
				total += w + 2
			}
			sb.WriteString(strings.Repeat("─", total-2))
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Inline spans

type span struct {
	Text   string
	Bold   bool
	Italic bool
	Code   bool
}

func parseInline(s string) []span {
	var out []span
	var cur strings.Builder
	bold, italic := false, false
	emit := func(code bool, txt string) {
		if txt == "" {
			return
		}
		out = append(out, span{Text: txt, Bold: bold && !code, Italic: italic && !code, Code: code})
	}
	flush := func() {
		emit(false, cur.String())
		cur.Reset()
	}
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\' && i+1 < len(r) && strings.ContainsRune("*_`\\[]", r[i+1]):
			cur.WriteRune(r[i+1])
			i++
		case c == '`':
			end := -1
			for j := i + 1; j < len(r); j++ {
				if r[j] == '`' {
					end = j
					break
				}
			}
			if end < 0 {
				cur.WriteRune(c)
				continue
			}
			flush()
			emit(true, string(r[i+1:end]))
			i = end
		case c == '*' && i+1 < len(r) && r[i+1] == '*':
			flush()
			bold = !bold
			i++
		case (c == '*' || c == '_') && (italic || (i+1 < len(r) && r[i+1] != ' ' && (i == 0 || r[i-1] == ' ' || r[i-1] == '('))):
			if c == '_' && !italic && i > 0 && r[i-1] != ' ' {
				cur.WriteRune(c)
				continue
			}
			flush()
			italic = !italic
		case c == '[':
			// [text](url) -> text (url shown dimmed is overkill; keep text + url)
			closeB := strings.IndexRune(string(r[i:]), ']')
			if closeB > 0 {
				cb := i + len([]rune(string(r[i:])[:closeB]))
				if cb+1 < len(r) && r[cb+1] == '(' {
					rest := string(r[cb+2:])
					if cp := strings.IndexRune(rest, ')'); cp >= 0 {
						label := string(r[i+1 : cb])
						url := rest[:cp]
						cur.WriteString(label)
						if url != label && !strings.HasPrefix(url, "#") {
							cur.WriteString(" (" + url + ")")
						}
						i = cb + 2 + len([]rune(rest[:cp]))
						continue
					}
				}
			}
			cur.WriteRune(c)
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}

func stripInline(s string) string {
	var sb strings.Builder
	for _, sp := range parseInline(s) {
		sb.WriteString(sp.Text)
	}
	return sb.String()
}
