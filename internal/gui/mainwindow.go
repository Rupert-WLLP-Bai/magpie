//go:build !nogui

package gui

import (
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/yetone/magpie/internal/settings"
)

// mainMacWindow keeps an open window on its Space. showMainWindow temporarily
// moves only a hidden window to the active Space when it is reopened.
func mainMacWindow() application.MacWindow {
	return application.MacWindow{
		// no InvisibleTitleBarHeight: that strip drags from anywhere in
		// it, tabs included; the header marks what drags instead
		TitleBar:           application.MacTitleBarHiddenInset,
		CollectionBehavior: application.MacWindowCollectionBehaviorFullScreenPrimary,
	}
}

type closeAction int

const (
	closeHide            closeAction = iota // hide the window now
	closeLeaveFullscreen                    // leave full screen, then hide it
)

// closeStep: what closing the main window does. A full-screen window on the
// Mac hidden as it is leaves its Space behind, black, with nothing in it; it
// leaves full screen first and is hidden once it has.
func closeStep(goos string, fullscreen bool) closeAction {
	if goos == "darwin" && fullscreen {
		return closeLeaveFullscreen
	}
	return closeHide
}

// hideMain hides the main window, and takes magpie out of the Dock if it is
// there only while the window is shown.
func (h *host) hideMain() {
	application.InvokeSync(func() {
		if h.main != nil {
			h.main.Hide()
		}
	})
	h.dock(settings.Load(), false)
}

// leaveFullscreenThenHide takes the closed full-screen window out of full
// screen; WindowDidExitFullScreen hides it. Should that never come (the
// system refused to leave), it is hidden after a while all the same.
func (h *host) leaveFullscreenThenHide() {
	h.closing.Store(true)
	application.InvokeSync(func() {
		if h.main != nil {
			h.main.UnFullscreen()
		}
	})
	time.AfterFunc(3*time.Second, func() {
		if h.closing.Swap(false) {
			application.InvokeAsync(h.hideMain)
		}
	})
}
