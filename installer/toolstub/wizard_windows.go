//go:build windows

package main

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
	"github.com/yakbd/yakbd-cli/installer/winhost"
)

// The wizard window (docs/installer-cli.md §12.5): one top-level window of class WizardClass with standard controls
// (BUTTON check boxes and push buttons, STATIC texts, a msctls_progress32 bar), whose pages are shown by showing
// and hiding controls. It draws toolsetup.Wizard and turns clicks into its Toggle; every rule is there. Keyboard
// navigation by IsDialogMessage (Tab between controls, Space ticks a box, Enter is the main button, Esc cancels);
// visual styles by the manifest's dependency on Common Controls 6 (installer/exeres); per-monitor DPI by the
// manifest's PerMonitorV2, the layout scaled by GetDpiForWindow and laid out again on WM_DPICHANGED.
//
// Microsoft Learn: Enabling Visual Styles, IsDialogMessageW, WM_DPICHANGED, GetDpiForWindow. The other functions'
// signatures, the constants' values and the field order of WNDCLASSEXW, MSG and INITCOMMONCONTROLSEX: Microsoft's
// windows crate 0.61.3
// (NOTICE). user32, gdi32 and comctl32 are loaded by name: winhost's init limits the search to System32, and the name
// lets the manifest's side-by-side dependency select Common Controls 6.

// WizardClass is the window class of the wizard; the guest checkers find the window by it.
const WizardClass = "YakbdSetupWizard"

// Control ids: a contract with the guest checkers (tools/instcheck), docs/installer-cli.md §12.5.
const (
	idMain   = 1 // IDOK: Next, Install/Change/Remove, Close
	idCancel = 2 // IDCANCEL
	idBack   = 3

	idEditor     = 101
	idCLI        = 102
	idPath       = 103
	idSettings   = 104
	idPathRemove = 105

	idTitle        = 201
	idInstalled    = 202
	idEditorNote   = 203
	idCLINote      = 204
	idSummary      = 205
	idProgressText = 206
	idFinish       = 207
	idProgress     = 301
)

var (
	user32w   = syscall.NewLazyDLL("user32.dll")
	gdi32w    = syscall.NewLazyDLL("gdi32.dll")
	comctl32w = syscall.NewLazyDLL("comctl32.dll")
	kernel32w = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassExW         = user32w.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32w.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32w.NewProc("DefWindowProcW")
	pGetMessageW              = user32w.NewProc("GetMessageW")
	pIsDialogMessageW         = user32w.NewProc("IsDialogMessageW")
	pTranslateMessage         = user32w.NewProc("TranslateMessage")
	pDispatchMessageW         = user32w.NewProc("DispatchMessageW")
	pPostQuitMessage          = user32w.NewProc("PostQuitMessage")
	pPostMessageW             = user32w.NewProc("PostMessageW")
	pSendMessageW             = user32w.NewProc("SendMessageW")
	pShowWindow               = user32w.NewProc("ShowWindow")
	pDestroyWindow            = user32w.NewProc("DestroyWindow")
	pSetWindowTextW           = user32w.NewProc("SetWindowTextW")
	pEnableWindow             = user32w.NewProc("EnableWindow")
	pGetDlgItem               = user32w.NewProc("GetDlgItem")
	pSetWindowPos             = user32w.NewProc("SetWindowPos")
	pGetDpiForWindow          = user32w.NewProc("GetDpiForWindow")
	pAdjustWindowRectExForDpi = user32w.NewProc("AdjustWindowRectExForDpi")
	pAdjustWindowRectEx       = user32w.NewProc("AdjustWindowRectEx")
	pSystemParametersInfoW    = user32w.NewProc("SystemParametersInfoW")
	pLoadCursorW              = user32w.NewProc("LoadCursorW")
	pSetFocus                 = user32w.NewProc("SetFocus")
	pSetForegroundWindow      = user32w.NewProc("SetForegroundWindow")
	pCreateFontW              = gdi32w.NewProc("CreateFontW")
	pDeleteObject             = gdi32w.NewProc("DeleteObject")
	pInitCommonControlsEx     = comctl32w.NewProc("InitCommonControlsEx")
	pGetModuleHandleW         = kernel32w.NewProc("GetModuleHandleW")
	pRtlMoveMemory            = kernel32w.NewProc("RtlMoveMemory")
)

const (
	wsCaption        = 0x00C00000
	wsSysMenu        = 0x00080000
	wsMinimizeBox    = 0x00020000
	wsChild          = 0x40000000
	wsVisible        = 0x10000000
	wsTabStop        = 0x00010000
	wsExControlParnt = 0x00010000
	bsPushButton     = 0
	bsDefPushButton  = 1
	bsCheckBox       = 2
	ssNoPrefix       = 0x80
	pbsMarquee       = 0x08
	pbmSetMarquee    = 0x040A
	bmGetCheck       = 0x00F0
	bmSetCheck       = 0x00F1
	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmSetFont        = 0x0030
	wmCommand        = 0x0111
	wmDpiChanged     = 0x02E0
	wmApp            = 0x8000
	dmGetDefID       = 0x0400
	dcHasDefID       = 0x534B
	bnClicked        = 0
	swShow           = 5
	swHide           = 0
	idcArrow         = 32512
	cwUseDefault     = 0x80000000
	iccProgressClass = 0x20
	colorBtnFace     = 15
	fwNormal         = 400
	fwSemiBold       = 600
	defaultCharset   = 1
	cleartypeQuality = 5
	swpNoZOrder      = 0x0004
	swpNoActivate    = 0x0010
	spiGetWorkArea   = 0x0030
	wmDone           = wmApp + 1
	winStyle         = wsCaption | wsSysMenu | wsMinimizeBox
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type rect struct{ left, top, right, bottom int32 }

type initCommonControlsEx struct{ dwSize, dwICC uint32 }

func w16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// geometry is one control's place in device-independent pixels (96 DPI), in a client area of clientW x clientH.
type geometry struct{ x, y, w, h int32 }

const clientW, clientH = 560, 420

// The pages.
const (
	pageChoose = iota
	pageReady
	pageProgress
	pageFinish
)

type control struct {
	id     int
	class  string
	style  uintptr
	g      geometry
	pages  []int // shown on these pages (and when its box is visible)
	bold   bool
	hwnd   uintptr
	boxID  string // the model's box
	noteOf string // a note of this box
}

type wizardUI struct {
	w       *toolsetup.Wizard
	exec    func(toolsetup.Request) (int, []string)
	hwnd    uintptr
	page    int
	dpi     uint32
	font    uintptr
	bold    uintptr
	ctrls   []*control
	code    int
	result  chan execResult
	verb    string // Install, Change, Remove
	running bool
}

type execResult struct {
	code  int
	notes []string
}

var ui *wizardUI

func (h *win) Wizard(w *toolsetup.Wizard, exec func(toolsetup.Request) (int, []string)) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := &wizardUI{w: w, exec: exec, code: cmdline.UserExit, result: make(chan execResult, 1)}
	switch {
	case w.Uninstall:
		u.verb = "Remove"
	case w.Change:
		u.verb = "Change"
	default:
		u.verb = "Install"
	}
	ui = u
	icc := initCommonControlsEx{dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})), dwICC: iccProgressClass}
	pInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
	inst, _, _ := pGetModuleHandleW.Call(0)
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	wc := wndClassEx{lpfnWndProc: syscall.NewCallback(wndProc), hInstance: inst, hCursor: cursor,
		hbrBackground: colorBtnFace + 1, lpszClassName: w16(WizardClass)}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, err := pCreateWindowExW.Call(wsExControlParnt, uintptr(unsafe.Pointer(w16(WizardClass))), uintptr(unsafe.Pointer(w16("YAKBD Setup"))),
		winStyle, cwUseDefault, cwUseDefault, clientW, clientH, 0, 0, inst, 0)
	if hwnd == 0 {
		h.Message(title, fmt.Sprintf("The setup window cannot be opened: %v", err), winhost.MsgError)
		return cmdline.InstallFailure
	}
	u.hwnd = hwnd
	u.build(inst)
	u.layout(true)
	u.show()
	pShowWindow.Call(hwnd, swShow)
	pSetForegroundWindow.Call(hwnd)
	pSetFocus.Call(u.item(idMain))
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if d, _, _ := pIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&m))); d != 0 {
			continue
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	if u.font != 0 {
		pDeleteObject.Call(u.font)
		pDeleteObject.Call(u.bold)
	}
	return u.code
}

func (u *wizardUI) item(id int) uintptr {
	r, _, _ := pGetDlgItem.Call(u.hwnd, uintptr(id))
	return r
}

// build creates every control once; show and sync set their texts, states and visibility.
func (u *wizardUI) build(inst uintptr) {
	cb := uintptr(wsTabStop | bsCheckBox)
	st := uintptr(ssNoPrefix)
	all := []int{pageChoose, pageReady, pageProgress, pageFinish}
	choose := []int{pageChoose}
	u.ctrls = []*control{
		{id: idTitle, class: "STATIC", style: st, g: geometry{24, 16, 512, 30}, pages: all, bold: true},
		{id: idInstalled, class: "STATIC", style: st, g: geometry{24, 52, 512, 40}, pages: []int{pageChoose, pageReady}},
		{id: idEditor, class: "BUTTON", style: cb, g: geometry{24, 104, 512, 22}, pages: choose, boxID: toolsetup.BoxEditor},
		{id: idEditorNote, class: "STATIC", style: st, g: geometry{44, 127, 492, 18}, pages: choose, noteOf: toolsetup.BoxEditor},
		{id: idCLI, class: "BUTTON", style: cb, g: geometry{24, 152, 512, 22}, pages: choose, boxID: toolsetup.BoxCLI},
		{id: idCLINote, class: "STATIC", style: st, g: geometry{44, 175, 492, 18}, pages: choose, noteOf: toolsetup.BoxCLI},
		{id: idPath, class: "BUTTON", style: cb, g: geometry{24, 208, 512, 22}, pages: choose, boxID: toolsetup.BoxPath},
		{id: idSettings, class: "BUTTON", style: cb, g: geometry{24, 208, 512, 22}, pages: choose, boxID: toolsetup.BoxSettings},
		{id: idPathRemove, class: "BUTTON", style: cb, g: geometry{24, 236, 512, 22}, pages: choose, boxID: toolsetup.BoxPathRemove},
		{id: idSummary, class: "STATIC", style: st, g: geometry{24, 104, 512, 240}, pages: []int{pageReady}},
		{id: idProgressText, class: "STATIC", style: st, g: geometry{24, 104, 512, 40}, pages: []int{pageProgress}},
		{id: idProgress, class: "msctls_progress32", style: pbsMarquee, g: geometry{24, 152, 512, 18}, pages: []int{pageProgress}},
		{id: idFinish, class: "STATIC", style: st, g: geometry{24, 104, 512, 250}, pages: []int{pageFinish}},
		{id: idBack, class: "BUTTON", style: wsTabStop | bsPushButton, g: geometry{256, 374, 92, 28}, pages: []int{pageChoose, pageReady}},
		{id: idMain, class: "BUTTON", style: wsTabStop | bsDefPushButton, g: geometry{356, 374, 92, 28}, pages: all},
		{id: idCancel, class: "BUTTON", style: wsTabStop | bsPushButton, g: geometry{456, 374, 80, 28}, pages: []int{pageChoose, pageReady, pageProgress}},
	}
	for _, c := range u.ctrls {
		c.hwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(w16(c.class))), uintptr(unsafe.Pointer(w16(""))),
			wsChild|c.style, 0, 0, 10, 10, u.hwnd, uintptr(c.id), inst, 0)
	}
	pSendMessageW.Call(u.item(idProgress), pbmSetMarquee, 1, 30)
}

func scale(v int32, dpi uint32) int32 { return int32((int64(v)*int64(dpi) + 48) / 96) }

// layout places the window and the controls for the window's DPI and sets the fonts (Segoe UI 9 pt, the title
// semibold 12 pt). first centres the window on the work area.
func (u *wizardUI) layout(first bool) {
	dpi := uintptr(96) // GetDpiForWindow is Windows 10 1607 and later; before it, 96
	if pGetDpiForWindow.Find() == nil {
		if d, _, _ := pGetDpiForWindow.Call(u.hwnd); d != 0 {
			dpi = d
		}
	}
	u.dpi = uint32(dpi)
	if u.font != 0 {
		pDeleteObject.Call(u.font)
		pDeleteObject.Call(u.bold)
	}
	font := func(pt int32, weight uintptr) uintptr {
		h := -int32((int64(pt)*int64(u.dpi) + 36) / 72)
		f, _, _ := pCreateFontW.Call(uintptr(h), 0, 0, 0, weight, 0, 0, 0, defaultCharset, 0, 0, cleartypeQuality, 0, uintptr(unsafe.Pointer(w16("Segoe UI"))))
		return f
	}
	u.font, u.bold = font(9, fwNormal), font(12, fwSemiBold)
	if first {
		r := rect{0, 0, scale(clientW, u.dpi), scale(clientH, u.dpi)}
		if pAdjustWindowRectExForDpi.Find() == nil {
			pAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&r)), winStyle, 0, wsExControlParnt, uintptr(u.dpi))
		} else {
			pAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), winStyle, 0, wsExControlParnt)
		}
		var wa rect
		pSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
		ww, wh := r.right-r.left, r.bottom-r.top
		x, y := wa.left+(wa.right-wa.left-ww)/2, wa.top+(wa.bottom-wa.top-wh)/2
		pSetWindowPos.Call(u.hwnd, 0, uintptr(x), uintptr(y), uintptr(ww), uintptr(wh), swpNoZOrder|swpNoActivate)
	}
	for _, c := range u.ctrls {
		g := c.g
		pSetWindowPos.Call(c.hwnd, 0, uintptr(scale(g.x, u.dpi)), uintptr(scale(g.y, u.dpi)), uintptr(scale(g.w, u.dpi)), uintptr(scale(g.h, u.dpi)), swpNoZOrder|swpNoActivate)
		f := u.font
		if c.bold {
			f = u.bold
		}
		pSendMessageW.Call(c.hwnd, wmSetFont, f, 1)
	}
}

func setText(hwnd uintptr, s string) { pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(w16(s)))) }

func (u *wizardUI) box(id string) *toolsetup.Box {
	w := u.w
	return map[string]*toolsetup.Box{toolsetup.BoxEditor: &w.Editor, toolsetup.BoxCLI: &w.CLI, toolsetup.BoxPath: &w.Path,
		toolsetup.BoxSettings: &w.Settings, toolsetup.BoxPathRemove: &w.PathRemove}[id]
}

// show draws the current page from the model.
func (u *wizardUI) show() {
	titles := map[string][4]string{
		"Install": {"Install YAKBD", "Ready to install", "Installing YAKBD", ""},
		"Change":  {"Change YAKBD", "Ready to change", "Changing YAKBD", ""},
		"Remove":  {"Remove YAKBD", "Ready to remove", "Removing YAKBD", ""},
	}[u.verb]
	if u.page != pageFinish {
		setText(u.item(idTitle), titles[u.page])
	}
	setText(u.item(idInstalled), strings.Join(u.w.Installed(), "\r\n"))
	for _, c := range u.ctrls {
		visible := false
		for _, p := range c.pages {
			visible = visible || p == u.page
		}
		if b := u.box(c.boxID); c.boxID != "" {
			visible = visible && b.Visible
			setText(c.hwnd, b.Label)
			check := uintptr(0)
			if b.Checked {
				check = 1
			}
			pSendMessageW.Call(c.hwnd, bmSetCheck, check, 0)
			pEnableWindow.Call(c.hwnd, boolArg(b.Enabled))
		}
		if b := u.box(c.noteOf); c.noteOf != "" {
			visible = visible && b.Visible && b.Note != ""
			setText(c.hwnd, b.Note)
		}
		pShowWindow.Call(c.hwnd, map[bool]uintptr{true: swShow, false: swHide}[visible])
	}
	main := map[int]string{pageChoose: "&Next >", pageReady: "&" + u.verb, pageProgress: u.verb, pageFinish: "&Close"}[u.page]
	setText(u.item(idMain), main)
	setText(u.item(idBack), "< &Back")
	setText(u.item(idCancel), "Cancel")
	pEnableWindow.Call(u.item(idBack), boolArg(u.page == pageReady))
	ok := true
	if u.page == pageChoose {
		ok = u.w.CanGo()
	}
	if u.page == pageReady {
		_, err := u.w.Plan()
		ok = err == nil
		setText(u.item(idSummary), strings.Join(u.w.Summary(), "\r\n\r\n"))
	}
	pEnableWindow.Call(u.item(idMain), boolArg(ok && u.page != pageProgress))
	pEnableWindow.Call(u.item(idCancel), boolArg(u.page != pageProgress))
	if u.page == pageProgress {
		setText(u.item(idProgressText), "Windows may ask for permission to make changes. Please wait.")
	}
}

func boolArg(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

var boxOfID = map[int]string{idEditor: toolsetup.BoxEditor, idCLI: toolsetup.BoxCLI, idPath: toolsetup.BoxPath,
	idSettings: toolsetup.BoxSettings, idPathRemove: toolsetup.BoxPathRemove}

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	u := ui
	switch m {
	case dmGetDefID:
		return dcHasDefID<<16 | idMain // Enter presses the main button (IsDialogMessage asks the window for it)
	case wmCommand:
		id, note := int(wp&0xFFFF), int(wp>>16&0xFFFF)
		if note != bnClicked {
			break
		}
		if box, ok := boxOfID[id]; ok && u.page == pageChoose {
			u.w.Toggle(box)
			u.show()
			return 0
		}
		switch id {
		case idBack:
			if u.page == pageReady {
				u.page = pageChoose
				u.show()
			}
		case idCancel:
			if !u.running {
				pDestroyWindow.Call(hwnd)
			}
		case idMain:
			u.next()
		}
		return 0
	case wmDone:
		r := <-u.result
		u.running = false
		u.code = r.code
		u.page = pageFinish
		setText(u.item(idTitle), finishTitle(u.verb, r.code))
		text := strings.Join(r.notes, "\r\n\r\n")
		if r.code == cmdline.UserExit {
			text = "Nothing was changed: Windows did not give the installer administrator rights."
		}
		setText(u.item(idFinish), strings.TrimSpace(text))
		u.show()
		pSetFocus.Call(u.item(idMain))
		return 0
	case wmDpiChanged:
		var r rect // the suggested window rectangle, copied out of lParam
		pRtlMoveMemory.Call(uintptr(unsafe.Pointer(&r)), lp, unsafe.Sizeof(r))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.left), uintptr(r.top), uintptr(r.right-r.left), uintptr(r.bottom-r.top), swpNoZOrder|swpNoActivate)
		if u.hwnd != 0 {
			u.layout(false)
		}
		return 0
	case wmClose:
		if u != nil && u.running {
			return 0 // the work runs; the window waits for it
		}
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
	return r
}

func finishTitle(verb string, code int) string {
	if !cmdline.Succeeded(code) {
		return "YAKBD Setup stopped"
	}
	return map[string]string{"Install": "YAKBD is installed", "Change": "YAKBD was changed", "Remove": "YAKBD was removed"}[verb]
}

// next is the main button: Next, then the work, then Close.
func (u *wizardUI) next() {
	switch u.page {
	case pageChoose:
		if u.w.CanGo() {
			u.page = pageReady
			u.show()
		}
	case pageReady:
		if _, err := u.w.Plan(); err != nil {
			return
		}
		u.page, u.running = pageProgress, true
		u.show()
		req := u.w.Request()
		hwnd := u.hwnd
		go func() {
			runtime.LockOSThread() // ShellExecuteExW wants an initialized COM apartment on its thread
			defer runtime.UnlockOSThread()
			coInit()
			code, notes := u.exec(req)
			u.result <- execResult{code, notes}
			pPostMessageW.Call(hwnd, wmDone, 0, 0)
		}()
	case pageFinish:
		pDestroyWindow.Call(u.hwnd)
	}
}
