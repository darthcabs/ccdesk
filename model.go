package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gioui.org/op/paint"
	"gioui.org/widget"
	_ "golang.org/x/image/webp"
)

// ---------------------------------------------------------------------------
// Accounts

type SessionInfo struct {
	ID        string
	CWD       string
	Title     string
	Updated   time.Time
	Acct      *Account
	click     widget.Clickable
	searchKey string
}

type Account struct {
	Idx    int
	Cfg    *AccountConfig
	Pal    Palette
	client *ACPClient
	env    []string // environment the connector (and Claude) runs with
	Status string   // "", "starting", error text
	Ready  bool

	Sessions []*SessionInfo
	Plan     string         // e.g. "Claude Pro", from the connector's auth status
	Commands []Command      // last known slash commands (for drafts)
	LastCfg  []ConfigOption // last known config options (for drafts)

	newBtn   widget.Clickable
	themeBtn widget.Clickable
	filter   widget.Clickable
}

// ---------------------------------------------------------------------------
// Conversation items

type ItemKind int

const (
	KUser ItemKind = iota
	KAgent
	KThought
	KTool
	KPlan
	KNotice
	KError
	KPerm
)

type Attachment struct {
	Mime string
	Data []byte
	img  image.Image
	op   paint.ImageOp
	ok   bool
	done bool
	copy widget.Clickable
	del  widget.Clickable
}

func (a *Attachment) Decode() bool {
	if a.done {
		return a.ok
	}
	a.done = true
	img, _, err := image.Decode(bytes.NewReader(a.Data))
	if err != nil {
		return false
	}
	a.img = img
	a.op = paint.NewImageOp(img)
	a.ok = true
	return true
}

type PlanEntry struct {
	Content string
	Status  string
}

type PermOption struct {
	ID   string
	Name string
	Kind string
	btn  widget.Clickable
}

type Item struct {
	Kind  ItemKind
	MsgID string
	Text  string
	Imgs  []*Attachment

	// tool calls
	ToolID   string
	Title    string
	ToolKind string
	Status   string
	Input    string
	Output   string
	Diff     string

	Plan []PlanEntry

	// permission request
	PermOpts  []*PermOption
	permCh    chan string
	PermDone  string
	PermTitle string

	// UI state
	expanded bool
	toggle   widget.Clickable
	copyBtn  widget.Clickable
	mt       msgText
	outSel   widget.Selectable
	inSel    widget.Selectable
}

type Mode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ConfigChoice struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ConfigOption struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Category     string         `json:"category"`
	Type         string         `json:"type"`
	CurrentValue string         `json:"currentValue"`
	Options      []ConfigChoice `json:"options"`
}

func (o *ConfigOption) currentName() string {
	for _, c := range o.Options {
		if c.Value == o.CurrentValue {
			return c.Name
		}
	}
	return o.CurrentValue
}

type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Session struct {
	Acct    *Account
	ID      string
	CWD     string
	Title   string
	Items   []*Item
	byTool  map[string]*Item
	Loading bool
	Loaded  bool
	Busy    bool
	// Suggestion is Claude's predicted next prompt (shown greyed in the composer).
	Suggestion string
	Err        string

	Modes    []Mode
	ModeID   string
	Commands []Command
	Config   []ConfigOption
	// values chosen on a draft, applied right after the session is created
	pendingCfg map[string]string

	list widget.List
	// stick to bottom while streaming
	follow bool
}

func newSession(a *Account, id, cwd, title string) *Session {
	s := &Session{Acct: a, ID: id, CWD: cwd, Title: title, byTool: map[string]*Item{}, follow: true}
	s.list.Axis = 1 // vertical
	s.list.ScrollToEnd = true
	return s
}

func (s *Session) last() *Item {
	if len(s.Items) == 0 {
		return nil
	}
	return s.Items[len(s.Items)-1]
}

func (s *Session) add(it *Item) *Item {
	s.Items = append(s.Items, it)
	return it
}

// ---------------------------------------------------------------------------
// ACP session/update handling

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Resource *struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"resource"`
}

type toolContent struct {
	Type    string        `json:"type"`
	Content *contentBlock `json:"content"`
	Path    string        `json:"path"`
	OldText *string       `json:"oldText"`
	NewText string        `json:"newText"`
}

type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
	MessageID     string          `json:"messageId"`

	ToolCallID string          `json:"toolCallId"`
	Title      *string         `json:"title"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	RawInput   json.RawMessage `json:"rawInput"`
	RawOutput  json.RawMessage `json:"rawOutput"`

	Entries []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"entries"`

	Meta              json.RawMessage `json:"_meta"`
	AvailableCommands []Command       `json:"availableCommands"`
	CurrentModeID     string          `json:"currentModeId"`
	ConfigOptions     []ConfigOption  `json:"configOptions"`
}

func (s *Session) applyUpdate(u *sessionUpdate) {
	switch u.SessionUpdate {
	case "user_message_chunk", "agent_message_chunk", "agent_thought_chunk":
		kind := KUser
		if u.SessionUpdate == "agent_message_chunk" {
			kind = KAgent
		} else if u.SessionUpdate == "agent_thought_chunk" {
			kind = KThought
		}
		if kind == KUser && s.Busy {
			// we already showed the prompt locally
			return
		}
		var cb contentBlock
		json.Unmarshal(u.Content, &cb)
		it := s.last()
		if it == nil || it.Kind != kind || (u.MessageID != "" && it.MsgID != "" && it.MsgID != u.MessageID) {
			it = s.add(&Item{Kind: kind, MsgID: u.MessageID})
		}
		switch cb.Type {
		case "text":
			it.Text += cb.Text
		case "image":
			if data, err := base64.StdEncoding.DecodeString(cb.Data); err == nil {
				it.Imgs = append(it.Imgs, &Attachment{Mime: cb.MimeType, Data: data})
			}
		case "resource_link":
			it.Text += fmt.Sprintf("\n[%s]", firstNonEmpty(cb.Name, cb.URI))
		case "resource":
			if cb.Resource != nil {
				it.Text += fmt.Sprintf("\n[%s]", cb.Resource.URI)
			}
		}
	case "tool_call", "tool_call_update":
		it := s.byTool[u.ToolCallID]
		if it == nil {
			it = s.add(&Item{Kind: KTool, ToolID: u.ToolCallID})
			s.byTool[u.ToolCallID] = it
		}
		if u.Title != nil {
			it.Title = *u.Title
		}
		if u.Kind != "" {
			it.ToolKind = u.Kind
		}
		if u.Status != "" {
			it.Status = u.Status
		}
		if len(u.RawInput) > 0 && string(u.RawInput) != "null" {
			it.Input = formatToolInput(u.RawInput)
		}
		if len(u.RawOutput) > 0 && string(u.RawOutput) != "null" {
			it.Output = formatRaw(u.RawOutput)
		}
		if len(u.Content) > 0 && string(u.Content) != "null" {
			var tcs []toolContent
			if json.Unmarshal(u.Content, &tcs) == nil {
				var txt []string
				for _, tc := range tcs {
					switch tc.Type {
					case "diff":
						old := ""
						if tc.OldText != nil {
							old = *tc.OldText
						}
						it.Diff = shortenHome(tc.Path) + "\n" + lineDiff(old, tc.NewText)
					case "content":
						if tc.Content != nil && tc.Content.Type == "text" {
							txt = append(txt, tc.Content.Text)
						}
					}
				}
				// Content that arrives with the tool input is its description;
				// later content is output (fenced as ```console in the connector's air mode).
				if len(txt) > 0 {
					joined := strings.Join(txt, "\n")
					hasInput := len(u.RawInput) > 0 && string(u.RawInput) != "null"
					if u.SessionUpdate == "tool_call" || hasInput {
						if it.Input == "" {
							it.Input = joined
						}
					} else if len(u.RawOutput) == 0 || string(u.RawOutput) == "null" {
						it.Output = stripFence(joined)
					}
				}
			}
		}
	case "session_info_update":
		var meta struct {
			PromptSuggestions *struct {
				Suggestions []struct {
					Text string `json:"text"`
				} `json:"suggestions"`
			} `json:"promptSuggestions"`
		}
		if len(u.Meta) > 0 && json.Unmarshal(u.Meta, &meta) == nil && meta.PromptSuggestions != nil {
			s.Suggestion = ""
			if len(meta.PromptSuggestions.Suggestions) > 0 {
				s.Suggestion = strings.TrimSpace(meta.PromptSuggestions.Suggestions[0].Text)
			}
		}
		if u.Title != nil && strings.TrimSpace(*u.Title) != "" {
			s.Title = strings.TrimSpace(*u.Title)
		}
	case "plan":
		it := s.last()
		if it == nil || it.Kind != KPlan {
			it = s.add(&Item{Kind: KPlan})
		}
		it.Plan = it.Plan[:0]
		for _, e := range u.Entries {
			it.Plan = append(it.Plan, PlanEntry{Content: e.Content, Status: e.Status})
		}
	case "available_commands_update":
		s.Commands = u.AvailableCommands
		s.Acct.Commands = u.AvailableCommands
	case "config_option_update":
		if len(u.ConfigOptions) > 0 {
			s.Config = u.ConfigOptions
			s.Acct.LastCfg = u.ConfigOptions
		}
	case "current_mode_update":
		s.ModeID = u.CurrentModeID
		for i := range s.Config {
			if s.Config[i].ID == "mode" {
				s.Config[i].CurrentValue = u.CurrentModeID
			}
		}
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func formatRaw(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// Arrays of content blocks ({type:text,text:...})
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) == nil && len(blocks) > 0 && blocks[0].Type != "" {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	}
	return string(raw)
}

func formatToolInput(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return formatRaw(raw)
	}
	if c, ok := m["command"].(string); ok {
		return "$ " + c
	}
	if p, ok := m["file_path"].(string); ok && len(m) <= 3 {
		return shortenHome(p)
	}
	if q, ok := m["pattern"].(string); ok {
		if p, ok := m["path"].(string); ok {
			return q + "  in " + shortenHome(p)
		}
		return q
	}
	return formatRaw(raw)
}

// lineDiff produces a compact +/- diff (LCS on lines, capped for size).
func lineDiff(a, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	if a == "" {
		al = nil
	}
	if len(al)*len(bl) > 4_000_000 {
		var sb strings.Builder
		for _, l := range al {
			sb.WriteString("- " + l + "\n")
		}
		for _, l := range bl {
			sb.WriteString("+ " + l + "\n")
		}
		return sb.String()
	}
	n, m := len(al), len(bl)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	type ln struct {
		op byte
		s  string
	}
	var out []ln
	i, j := 0, 0
	for i < n && j < m {
		if al[i] == bl[j] {
			out = append(out, ln{' ', al[i]})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			out = append(out, ln{'-', al[i]})
			i++
		} else {
			out = append(out, ln{'+', bl[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, ln{'-', al[i]})
	}
	for ; j < m; j++ {
		out = append(out, ln{'+', bl[j]})
	}
	// keep 2 lines of context around changes
	keep := make([]bool, len(out))
	for k, l := range out {
		if l.op != ' ' {
			for d := -2; d <= 2; d++ {
				if k+d >= 0 && k+d < len(out) {
					keep[k+d] = true
				}
			}
		}
	}
	var sb strings.Builder
	gap := false
	for k, l := range out {
		if !keep[k] {
			gap = true
			continue
		}
		if gap && sb.Len() > 0 {
			sb.WriteString("  …\n")
		}
		gap = false
		sb.WriteByte(l.op)
		sb.WriteByte(' ')
		sb.WriteString(l.s)
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ---------------------------------------------------------------------------
// App state shared between the UI and connector goroutines (guarded by mu).

type App struct {
	mu       sync.Mutex
	cfg      Config
	accounts []*Account
	sessions map[string]*Session // open sessions by "acctIdx/id"
	active   *Session
	// account used for the "new session" draft when no session is active
	draftAcct  *Account
	invalidate func()
	focused    bool
	toast      string
	toastUntil time.Time
}

func skey(a *Account, id string) string { return fmt.Sprintf("%d/%s", a.Idx, id) }

func (app *App) notifyUser(title, body string) {
	if app.focused {
		return
	}
	go runQuiet("notify-send", "-a", "ccdesk", title, body)
}

func (app *App) setToast(s string) {
	app.toast = s
	app.toastUntil = time.Now().Add(2 * time.Second)
	go func() {
		time.Sleep(2100 * time.Millisecond)
		app.invalidate()
	}()
}

func (app *App) startAccount(a *Account) {
	app.mu.Lock()
	a.Status = "starting"
	a.Ready = false
	app.mu.Unlock()
	app.invalidate()

	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") && !strings.HasPrefix(e, "CLAUDECODE=") && !strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") {
			filtered = append(filtered, e)
		}
	}
	env = filtered
	if a.Cfg.ConfigDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+expandHome(a.Cfg.ConfigDir))
	}
	logDir := filepath.Join(home(), ".cache", "ccdesk")
	os.MkdirAll(logDir, 0o755)
	a.env = env
	c, err := StartACP(expandHome(app.cfg.Connector), env, filepath.Join(logDir, strings.ToLower(a.Cfg.Name)+".log"))
	if err != nil {
		app.mu.Lock()
		a.Status = "connector failed: " + err.Error()
		app.mu.Unlock()
		app.invalidate()
		return
	}
	c.OnNotify = func(method string, params json.RawMessage) { app.onNotify(a, method, params) }
	c.OnRequest = func(method string, params json.RawMessage) (any, *rpcError) { return app.onRequest(a, method, params) }
	a.client = c
	err = c.Call("initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
			// The connector only forwards prompt suggestions to clients using its
			// JetBrains "air" extension. Side effect: command output arrives fenced.
			"_meta": map[string]any{"jetbrains": map[string]any{"air": map[string]any{"version": 1, "capabilities": []string{}}}},
		},
		"clientInfo": map[string]any{"name": "ccdesk", "version": "0.1"},
	}, nil)
	app.mu.Lock()
	if err != nil {
		a.Status = "initialize failed: " + err.Error()
	} else {
		a.Status = ""
		a.Ready = true
	}
	app.mu.Unlock()
	app.invalidate()
	if err == nil {
		app.refreshSessions(a)
	}
}

func (app *App) refreshSessions(a *Account) {
	if a.client == nil {
		return
	}
	var all []*SessionInfo
	cursor := ""
	for page := 0; page < 50; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Sessions []struct {
				SessionID string `json:"sessionId"`
				CWD       string `json:"cwd"`
				Title     string `json:"title"`
				UpdatedAt string `json:"updatedAt"`
			} `json:"sessions"`
			NextCursor string `json:"nextCursor"`
		}
		if err := a.client.Call("session/list", params, &res); err != nil {
			app.mu.Lock()
			a.Status = "listing failed: " + err.Error()
			app.mu.Unlock()
			break
		}
		for _, s := range res.Sessions {
			t, _ := time.Parse(time.RFC3339Nano, s.UpdatedAt)
			title := strings.TrimSpace(s.Title)
			if title == "" {
				title = "(untitled)"
			}
			all = append(all, &SessionInfo{ID: s.SessionID, CWD: s.CWD, Title: title, Updated: t, Acct: a,
				searchKey: strings.ToLower(title + " " + s.CWD)})
		}
		cursor = res.NextCursor
		if cursor == "" {
			break
		}
	}
	app.mu.Lock()
	// keep click state for existing rows
	old := map[string]*SessionInfo{}
	for _, s := range a.Sessions {
		old[s.ID] = s
	}
	for i, s := range all {
		if o := old[s.ID]; o != nil {
			o.Title, o.CWD, o.Updated, o.searchKey = s.Title, s.CWD, s.Updated, s.searchKey
			all[i] = o
		}
	}
	a.Sessions = all
	if want := os.Getenv("CCDESK_OPEN"); want != "" && app.active == nil {
		for _, si := range all {
			if strings.Contains(si.Title, want) {
				app.openSession(si)
				break
			}
		}
	}
	app.mu.Unlock()
	app.invalidate()
}

func (app *App) findSession(a *Account, id string) *Session {
	return app.sessions[skey(a, id)]
}

func (app *App) onNotify(a *Account, method string, params json.RawMessage) {
	if method == "_auth/status_update" {
		var p struct {
			AuthStatus struct {
				Label string `json:"label"`
			} `json:"authStatus"`
		}
		if json.Unmarshal(params, &p) == nil && p.AuthStatus.Label != "" {
			app.mu.Lock()
			a.Plan = p.AuthStatus.Label
			app.mu.Unlock()
			app.invalidate()
		}
		return
	}
	if method != "session/update" {
		return
	}
	var p struct {
		SessionID string        `json:"sessionId"`
		Update    sessionUpdate `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	app.mu.Lock()
	if s := app.findSession(a, p.SessionID); s != nil {
		s.applyUpdate(&p.Update)
	}
	app.mu.Unlock()
	app.invalidate()
}

func (app *App) onRequest(a *Account, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "session/request_permission":
		var p struct {
			SessionID string `json:"sessionId"`
			ToolCall  struct {
				ToolCallID string          `json:"toolCallId"`
				Title      string          `json:"title"`
				RawInput   json.RawMessage `json:"rawInput"`
			} `json:"toolCall"`
			Options []struct {
				OptionID string `json:"optionId"`
				Name     string `json:"name"`
				Kind     string `json:"kind"`
			} `json:"options"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		ch := make(chan string, 1)
		app.mu.Lock()
		s := app.findSession(a, p.SessionID)
		if s == nil {
			app.mu.Unlock()
			return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
		}
		title := p.ToolCall.Title
		if t := s.byTool[p.ToolCall.ToolCallID]; t != nil && title == "" {
			title = t.Title
		}
		it := &Item{Kind: KPerm, permCh: ch, PermTitle: title}
		if len(p.ToolCall.RawInput) > 0 && string(p.ToolCall.RawInput) != "null" {
			it.Input = formatToolInput(p.ToolCall.RawInput)
		}
		for _, o := range p.Options {
			it.PermOpts = append(it.PermOpts, &PermOption{ID: o.OptionID, Name: o.Name, Kind: o.Kind})
		}
		s.add(it)
		s.follow = true
		app.mu.Unlock()
		app.notifyUser(a.Cfg.Name+": permission needed", title)
		app.invalidate()
		choice := <-ch
		if choice == "" {
			return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
		}
		return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": choice}}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not supported by ccdesk: " + method}
}

// openSession loads (replays) an existing session.
func (app *App) openSession(info *SessionInfo) {
	a := info.Acct
	k := skey(a, info.ID)
	if s := app.sessions[k]; s != nil {
		app.active = s
		return
	}
	s := newSession(a, info.ID, info.CWD, info.Title)
	s.Loading = true
	app.sessions[k] = s
	app.active = s
	go func() {
		var res struct {
			Modes *struct {
				CurrentModeID  string `json:"currentModeId"`
				AvailableModes []Mode `json:"availableModes"`
			} `json:"modes"`
			ConfigOptions []ConfigOption `json:"configOptions"`
		}
		err := a.client.Call("session/load", map[string]any{"sessionId": info.ID, "cwd": info.CWD, "mcpServers": []any{}, "_meta": app.sessionMeta(a)}, &res)
		app.mu.Lock()
		s.Loading = false
		if err != nil {
			s.Err = err.Error()
			s.add(&Item{Kind: KError, Text: "Could not open session: " + err.Error()})
		} else {
			s.Loaded = true
			if res.Modes != nil {
				s.Modes = res.Modes.AvailableModes
				if s.ModeID == "" {
					s.ModeID = res.Modes.CurrentModeID
				}
			}
			if len(res.ConfigOptions) > 0 {
				s.Config = res.ConfigOptions
				a.LastCfg = res.ConfigOptions
			}
		}
		s.list.Position.BeforeEnd = false
		if os.Getenv("CCDESK_DEBUG") != "" {
			for i, it := range s.Items {
				fmt.Fprintf(os.Stderr, "item %d kind=%d text=%q imgs=%d\n", i, it.Kind, firstN(it.Text, 60), len(it.Imgs))
			}
			fmt.Fprintf(os.Stderr, "loaded %d items err=%v\n", len(s.Items), err)
		}
		app.mu.Unlock()
		app.invalidate()
	}()
}

// sendPrompt sends a prompt. For a draft session (ID==""), a new session is created first.
func (app *App) sendPrompt(s *Session, text string, imgs []*Attachment) {
	var blocks []map[string]any
	for _, im := range imgs {
		blocks = append(blocks, map[string]any{"type": "image", "mimeType": im.Mime, "data": base64.StdEncoding.EncodeToString(im.Data)})
	}
	if strings.TrimSpace(text) != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	if len(blocks) == 0 {
		return
	}
	s.add(&Item{Kind: KUser, Text: text, Imgs: imgs})
	s.Suggestion = ""
	s.Busy = true
	s.follow = true
	a := s.Acct
	go func() {
		if s.ID == "" {
			var res struct {
				SessionID string `json:"sessionId"`
				Modes     *struct {
					CurrentModeID  string `json:"currentModeId"`
					AvailableModes []Mode `json:"availableModes"`
				} `json:"modes"`
				ConfigOptions []ConfigOption `json:"configOptions"`
			}
			err := a.client.Call("session/new", map[string]any{"cwd": s.CWD, "mcpServers": []any{}, "_meta": app.sessionMeta(a)}, &res)
			app.mu.Lock()
			if err != nil {
				s.Busy = false
				s.add(&Item{Kind: KError, Text: "Could not start session: " + err.Error()})
				app.mu.Unlock()
				app.invalidate()
				return
			}
			s.ID = res.SessionID
			s.Loaded = true
			if res.Modes != nil {
				s.Modes = res.Modes.AvailableModes
				s.ModeID = res.Modes.CurrentModeID
			}
			if len(res.ConfigOptions) > 0 {
				s.Config = res.ConfigOptions
				a.LastCfg = res.ConfigOptions
			}
			app.sessions[skey(a, s.ID)] = s
			pending := s.pendingCfg
			s.pendingCfg = nil
			app.mu.Unlock()
			// apply choices made on the draft (model, mode) before the first prompt
			for id, v := range pending {
				app.setConfigSync(s, id, v)
			}
		}
		var res struct {
			StopReason string `json:"stopReason"`
		}
		err := a.client.Call("session/prompt", map[string]any{"sessionId": s.ID, "prompt": blocks}, &res)
		app.mu.Lock()
		s.Busy = false
		// cancel any unanswered permission prompts
		for _, it := range s.Items {
			if it.Kind == KPerm && it.PermDone == "" && it.permCh != nil {
				it.PermDone = "cancelled"
				select {
				case it.permCh <- "":
				default:
				}
			}
		}
		if err != nil {
			s.add(&Item{Kind: KError, Text: err.Error()})
		} else if res.StopReason != "" && res.StopReason != "end_turn" {
			s.add(&Item{Kind: KNotice, Text: "Stopped: " + strings.ReplaceAll(res.StopReason, "_", " ")})
		}
		title := s.Title
		app.mu.Unlock()
		app.notifyUser(a.Cfg.Name+": Claude finished", title)
		app.invalidate()
		app.refreshSessions(a)
		// pick up the generated title for new sessions
		app.mu.Lock()
		for _, si := range a.Sessions {
			if si.ID == s.ID && si.Title != "(untitled)" {
				s.Title = si.Title
			}
		}
		app.mu.Unlock()
		app.invalidate()
	}()
}

func (app *App) cancel(s *Session) {
	if s.ID == "" || !s.Busy {
		return
	}
	go s.Acct.client.Notify("session/cancel", map[string]any{"sessionId": s.ID})
}

// setConfig changes a session option (model, mode, ...). For drafts it is remembered
// and applied when the session is created.
func (app *App) setConfig(s *Session, id, value string) {
	for i := range s.Config {
		if s.Config[i].ID == id {
			s.Config[i].CurrentValue = value
		}
	}
	if id == "mode" {
		s.ModeID = value
	}
	if s.ID == "" {
		if s.pendingCfg == nil {
			s.pendingCfg = map[string]string{}
		}
		s.pendingCfg[id] = value
		return
	}
	go app.setConfigSync(s, id, value)
}

func (app *App) setConfigSync(s *Session, id, value string) {
	var res struct {
		ConfigOptions []ConfigOption `json:"configOptions"`
	}
	err := s.Acct.client.Call("session/set_config_option", map[string]any{"sessionId": s.ID, "configId": id, "value": value}, &res)
	app.mu.Lock()
	if err != nil {
		s.add(&Item{Kind: KError, Text: "Could not change " + id + ": " + err.Error()})
	} else if len(res.ConfigOptions) > 0 {
		s.Config = res.ConfigOptions
		s.Acct.LastCfg = res.ConfigOptions
	}
	app.mu.Unlock()
	app.invalidate()
}

func (app *App) setMode(s *Session, id string) {
	if s.ID == "" {
		return
	}
	s.ModeID = id
	go func() {
		if err := s.Acct.client.Call("session/set_mode", map[string]any{"sessionId": s.ID, "modeId": id}, nil); err != nil {
			app.mu.Lock()
			s.add(&Item{Kind: KError, Text: "Could not change mode: " + err.Error()})
			app.mu.Unlock()
			app.invalidate()
		}
	}()
}

// allSessions returns every listed session across accounts, newest first.
func (app *App) allSessions(filterAcct int, query string) []*SessionInfo {
	var out []*SessionInfo
	q := strings.ToLower(strings.TrimSpace(query))
	for _, a := range app.accounts {
		if filterAcct >= 0 && a.Idx != filterAcct {
			continue
		}
		for _, s := range a.Sessions {
			if q != "" && !strings.Contains(s.searchKey, q) {
				continue
			}
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

func firstN(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// sessionMeta passes per-session options to the connector. Claude Code only
// generates prompt suggestions in SDK mode when this variable is set, and the
// connector doesn't forward its own environment for it, so we pass the full env.
func (app *App) sessionMeta(a *Account) map[string]any {
	if !app.cfg.suggestionsOn() {
		return nil
	}
	env := map[string]string{}
	for _, kv := range a.env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	env["CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION"] = "true"
	return map[string]any{"claudeCode": map[string]any{"options": map[string]any{"env": env}}}
}

// stripFence removes a code fence wrapping the whole text.
func stripFence(t string) string {
	s := strings.TrimSpace(t)
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") || len(s) < 6 {
		return t
	}
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return t
	}
	return strings.TrimSuffix(strings.TrimSuffix(s[nl+1:], "```"), "\n")
}
