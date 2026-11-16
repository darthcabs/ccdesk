package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"
)

func readCloser(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

func main() {
	cfg := loadConfig()
	a := &App{cfg: cfg, sessions: map[string]*Session{}}
	for i := range a.cfg.Accounts {
		ac := &a.cfg.Accounts[i]
		a.accounts = append(a.accounts, &Account{Idx: i, Cfg: ac, Pal: paletteFor(*ac)})
	}
	if len(a.accounts) == 0 {
		log.Fatal("no accounts configured in ", configPath())
	}

	scale := cfg.UIScale
	if scale <= 0 {
		scale = 1
	}

	go func() {
		w := new(app.Window)
		w.Option(app.Title("ccdesk"), app.Size(unit.Dp(1400), unit.Dp(950)))
		a.invalidate = w.Invalidate
		for _, acct := range a.accounts {
			go a.startAccount(acct)
		}
		go watchTheme(a)
		if n := os.Getenv("CCDESK_NEW"); n != "" { // debug: start on a new-session draft
			for _, acct := range a.accounts {
				if acct.Cfg.Name == n {
					a.draftAcct = acct
				}
			}
		}
		if err := run(w, a, scale); err != nil {
			log.Println(err)
		}
		for _, acct := range a.accounts {
			if acct.client != nil {
				acct.client.Close()
			}
		}
		os.Exit(0)
	}()
	app.Main()
}

// watchTheme reloads palettes that follow the current Omarchy theme when it changes.
// watchTheme reloads palettes that follow the system: Omarchy's current theme
// ("current") or the desktop light/dark setting ("auto").
func watchTheme(a *App) {
	p := filepath.Join(home(), ".local/state/omarchy/current/theme.name")
	var last time.Time
	if st, err := os.Stat(p); err == nil {
		last = st.ModTime()
	}
	lastDark := systemDark()
	for range time.Tick(2 * time.Second) {
		omarchyChanged := false
		if st, err := os.Stat(p); err == nil && st.ModTime().After(last) {
			last = st.ModTime()
			omarchyChanged = true
			time.Sleep(500 * time.Millisecond) // let the theme switch finish writing files
		}
		a.mu.Lock()
		usesAuto := false
		for _, acct := range a.accounts {
			usesAuto = usesAuto || acct.Cfg.Theme == "auto"
		}
		a.mu.Unlock()
		darkChanged := false
		if usesAuto {
			if d := systemDark(); d != lastDark {
				lastDark, darkChanged = d, true
			}
		}
		if !omarchyChanged && !darkChanged {
			continue
		}
		a.mu.Lock()
		for _, acct := range a.accounts {
			t := acct.Cfg.Theme
			if (omarchyChanged && (t == "current" || t == "")) || (darkChanged && t == "auto") {
				acct.Pal = paletteFor(*acct.Cfg)
			}
		}
		a.mu.Unlock()
		a.invalidate()
	}
}

func run(w *app.Window, a *App, scale float32) error {
	u := newUI(a)
	if v := os.Getenv("CCDESK_PICKER"); v != "" { // debug: start on an account's theme picker
		for _, acct := range a.accounts {
			if acct.Cfg.Name == v {
				u.openPicker(acct)
			}
		}
	}
	if v := os.Getenv("CCDESK_ACCOUNTS"); v != "" { // debug: start on the Accounts screen
		u.openAccounts()
		if v == "add" || v == "addsave" {
			u.addAccountDraft()
		}
		if v == "addsave" {
			go func() {
				time.Sleep(4 * time.Second)
				a.mu.Lock()
				u.saveAccounts()
				a.mu.Unlock()
				a.invalidate()
			}()
		}
	}
	app.ExternalDragHook = func(active bool) {
		a.mu.Lock()
		u.dragOver = active
		a.mu.Unlock()
		a.invalidate()
	}
	app.ExternalDropHook = func(mime string, data []byte) {
		a.mu.Lock()
		u.handleDrop(mime, data)
		a.mu.Unlock()
		a.invalidate()
	}
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.ConfigEvent:
			a.mu.Lock()
			a.focused = e.Config.Focused
			a.mu.Unlock()
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			if os.Getenv("CCDESK_DEBUG") != "" {
				log.Printf("metric px/dp=%.3f size=%v", gtx.Metric.PxPerDp, e.Size)
			}
			gtx.Metric.PxPerDp *= scale
			gtx.Metric.PxPerSp *= scale
			a.mu.Lock()
			u.Layout(gtx)
			a.mu.Unlock()
			e.Frame(gtx.Ops)
		}
	}
}
