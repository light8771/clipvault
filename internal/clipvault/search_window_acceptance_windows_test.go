//go:build windows

package clipvault

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// Opt-in real-desktop fixture; never takes focus or writes the clipboard in
// ordinary tests. Run with CLIPVAULT_POPUP_ACCEPTANCE=1 and -timeout=15m.
// Ctrl+Alt+F12 opens the production popup from the current foreground app.
// Ctrl+Alt+F10 toggles Ctrl+Enter / Alt+Enter paste; Ctrl+Alt+F11 exits.
// Uses only synthetic records in a fresh temporary database, with no recorder.
func TestSearchPopupDesktopAcceptance(t *testing.T) {
	if os.Getenv("CLIPVAULT_POPUP_ACCEPTANCE") != "1" {
		t.Skip("opt-in desktop acceptance fixture")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	i18n, err := NewI18nService(store, cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	previous := templateI18nService.Swap(i18n)
	defer templateI18nService.Store(previous)
	for i, text := range []string{"验收第二条：ALT-PASTE-456", "验收第一条：CTRL-PASTE-123"} {
		if err := store.Save(context.Background(), ClipboardItem{
			CapturedAtMS: time.Now().UnixMilli() + int64(i), RecordType: RecordTypeContent,
			Kind: "text", Format: "CF_UNICODETEXT", Text: text, MetaJSON: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("ACCEPTANCE_BROWSER_REQUEST=%s\n", r.URL.String())
		fmt.Fprint(w, "ClipVault popup acceptance: browser action received.")
	}))
	defer server.Close()
	for id, key := range []uintptr{0x7B, 0x79, 0x7A} { // F12, F10, F11
		if ok, _, err := procRegisterHotKey.Call(0, uintptr(0x4310+id), 0x4003, key); ok == 0 {
			t.Fatal(err)
		}
		defer procUnregisterHotKey.Call(0, uintptr(0x4310+id))
	}
	fmt.Printf("ACCEPTANCE_READY logs=%s\n", filepath.Join(dir, "logs"))
	var message msg
	for {
		ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(ret) <= 0 {
			t.Fatalf("message loop ended: %v", err)
		}
		if message.Message != wmHotkey {
			continue
		}
		switch message.WParam {
		case 0x4310:
			ShowSearchWindow(store, server.URL, logger, nil, cfg, Environment{DataDir: dir})
			// Desktop automation inventories omit WS_EX_TOOLWINDOW. Expose
			// this fixture's popup in that inventory; keep production behavior,
			// controls, input handling and foreground handoff unchanged.
			go func() {
				for attempt := 0; attempt < 100; attempt++ {
					searchWindowMu.Lock()
					sw := activeSearchWindow
					searchWindowMu.Unlock()
					if sw != nil {
						sw.mu.Lock()
						hwnd := sw.hwnd
						sw.mu.Unlock()
						if hwnd != 0 {
							const exStyleIndex = ^uintptr(19) // GWL_EXSTYLE (-20)
							user32.NewProc("SetWindowLongPtrW").Call(hwnd, exStyleIndex, wsExTopmost|0x40000)
							fmt.Println("ACCEPTANCE_POPUP_READY")
							return
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
			}()
		case 0x4311:
			updated := cfg.Get()
			if updated.Popup.KeyPaste == "Ctrl+Enter" {
				updated.Popup.KeyPaste = "Alt+Enter"
			} else {
				updated.Popup.KeyPaste = "Ctrl+Enter"
			}
			if err := cfg.Update(updated); err != nil {
				t.Fatal(err)
			}
			fmt.Printf("ACCEPTANCE_PASTE_KEY=%s\n", updated.Popup.KeyPaste)
		case 0x4312:
			return
		}
	}
}
