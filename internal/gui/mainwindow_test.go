//go:build !nogui

package gui

import (
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

// StringKe on Discord: a full-screen window closed left a black Space
// behind. On the Mac it leaves full screen before it is hidden.
func TestCloseStepLeavesFullscreen(t *testing.T) {
	cases := []struct {
		goos       string
		fullscreen bool
		want       closeAction
	}{
		{"darwin", true, closeLeaveFullscreen},
		{"darwin", false, closeHide},
		{"windows", true, closeHide},
		{"linux", true, closeHide},
		{"linux", false, closeHide},
	}
	for _, c := range cases {
		if got := closeStep(c.goos, c.fullscreen); got != c.want {
			t.Errorf("closeStep(%s, fullscreen=%v) = %v, want %v", c.goos, c.fullscreen, got, c.want)
		}
	}
}

// lainbo on v0.1.819 (#763): with the Dock set to hidden, a full-screen window
// had no close, minimise or full-screen buttons, and only Esc took it out of
// full screen. A full-screen window keeps magpie in the Dock, whatever the setting.
func TestFullscreenKeepsDock(t *testing.T) {
	hidden, window, shown := settings.Settings{}, settings.Settings{DockWindow: true}, settings.Settings{Dock: true}
	cases := []struct {
		s                 settings.Settings
		shown, fullscreen bool
		want              bool
	}{
		{hidden, true, true, true},
		{hidden, true, false, false},
		{hidden, false, false, false},
		{window, true, true, true},
		{window, true, false, true},
		{window, false, false, false},
		{shown, false, false, true},
	}
	for _, c := range cases {
		if got := inDock(c.s, c.shown, c.fullscreen); got != c.want {
			t.Errorf("inDock(%+v, shown=%v, fullscreen=%v) = %v, want %v", c.s, c.shown, c.fullscreen, got, c.want)
		}
	}
}
