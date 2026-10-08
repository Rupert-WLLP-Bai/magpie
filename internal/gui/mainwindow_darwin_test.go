//go:build darwin && cgo && !nogui

package gui

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/yetone/magpie/internal/settings"
)

// An already-open window must not migrate to the active Space when ordered
// front. A hidden or recreated window still opens on the current Space.
// Observe AppKit's actual ordering calls, not the constructor's options.
func TestMainWindowSpaceActivation(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "spaces.dylib")
	cmd := exec.Command("clang", "-dynamiclib", "-framework", "Cocoa", "testdata/mainwindow_spaces.m", "-o", probe)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build AppKit observer: %v\n%s", err, out)
	}
	out, err := runAppKit(t, 25*time.Second, []string{"MAGPIE_TEST_MAIN_SPACES=1", "DYLD_INSERT_LIBRARIES=" + probe})
	if err != nil {
		t.Fatalf("main window activation: %v\n%s", err, out)
	}
	for _, c := range []struct {
		stage string
		want  string
	}{
		{"first", "space order: visible=0 minimised=0 move=1 primary=1"},
		{"existing", "space order: visible=1 minimised=0 move=0 primary=1"},
		{"reopened", "space order: visible=0 minimised=0 move=1 primary=1"},
		{"recreated", "space order: visible=0 minimised=0 move=1 primary=1"},
	} {
		_, rest, ok := strings.Cut(string(out), "stage: "+c.stage+"\n")
		if !ok {
			t.Fatalf("missing %s activation\n%s", c.stage, out)
		}
		rest, _, _ = strings.Cut(rest, "stage: ")
		if !strings.Contains(rest, c.want) {
			t.Errorf("%s activation: want %s\n%s", c.stage, c.want, rest)
		}
	}
	t.Logf("AppKit activation trace:\n%s", out)
}

func init() {
	if os.Getenv("MAGPIE_TEST_MAIN_SPACES") != "1" {
		return
	}
	if err := settings.Save(settings.Settings{Dock: true}); err != nil {
		panic(err)
	}
	app := application.New(application.Options{
		Name: "magpie Space regression",
		Mac:  application.MacOptions{ActivationPolicy: application.ActivationPolicyRegular},
		Assets: application.AssetOptions{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body>Window activation regression</body></html>`)
		})},
	})
	h := &host{app: app}
	h.main = h.makeMain("/")
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			application.InvokeSync(func() {
				fmt.Println("stage: first")
				h.openMain("")
				fmt.Println("stage: existing")
				h.ShowMain("") // the Dock reopen hook's entry point
				fmt.Println("stage: reopened")
				h.hideMain()
				h.ShowMain("")
				fmt.Println("stage: recreated")
				h.hideMain()
				old := h.main
				h.gone.Store(old)
				dropWebView(old)
				old.Close()
				h.main = nil
				h.ShowMain("")
				if h.main == old {
					panic("released main window was not recreated")
				}
			})
			app.Quit()
		}()
	})
	if err := app.Run(); err != nil {
		panic(err)
	}
	os.Exit(0)
}
