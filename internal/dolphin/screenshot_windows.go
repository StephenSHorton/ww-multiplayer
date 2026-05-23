//go:build windows

package dolphin

import (
	"fmt"
	"image"
	"syscall"
	"time"
	"unsafe"
)

// Captures Dolphin's window via PrintWindow + a top-down 32-bit DIB
// section. PrintWindow with PW_RENDERFULLCONTENT (Windows 8.1+) is the
// documented way to grab DirectX/Vulkan-composited content — plain
// BitBlt from the window DC comes back black under those backends.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procEnumWindows               = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID  = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible           = user32.NewProc("IsWindowVisible")
	procGetWindowRect             = user32.NewProc("GetWindowRect")
	procGetWindowTextW            = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW      = user32.NewProc("GetWindowTextLengthW")
	procGetClassNameW             = user32.NewProc("GetClassNameW")
	procGetDesktopWindow          = user32.NewProc("GetDesktopWindow")
	procGetDC                     = user32.NewProc("GetDC")
	procReleaseDC                 = user32.NewProc("ReleaseDC")
	procPrintWindow               = user32.NewProc("PrintWindow")
	procPostMessageW              = user32.NewProc("PostMessageW")
	procSendMessageW              = user32.NewProc("SendMessageW")
	procMapVirtualKeyW            = user32.NewProc("MapVirtualKeyW")
	procSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	procShowWindow                = user32.NewProc("ShowWindow")
	procSendInput                 = user32.NewProc("SendInput")
	procGetFocus                  = user32.NewProc("GetFocus")
	procGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	procEnumChildWindows          = user32.NewProc("EnumChildWindows")
	procAttachThreadInput         = user32.NewProc("AttachThreadInput")
	procGetCurrentThreadId        = kernel32.NewProc("GetCurrentThreadId")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
)

const (
	pwRenderFullContent = 0x00000002

	dibRGBColors = 0
	biRGB        = 0
)

type rect struct {
	Left, Top, Right, Bottom int32
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [3]uint32 // unused for biRGB but keeps the struct shaped like the C version
}

// FindWindowByPID walks all top-level windows and returns the first
// visible one owned by the given process id. Dolphin's Qt main window
// is the only visible top-level window per process, so first-match is
// fine.
func FindWindowByPID(pid uint32) (uintptr, error) {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var winPID uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&winPID)))
		if winPID != pid {
			return 1 // keep iterating
		}
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		// Skip zero-area windows (Qt creates invisible helper top-levels).
		var r rect
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		if r.Right-r.Left <= 0 || r.Bottom-r.Top <= 0 {
			return 1
		}
		found = hwnd
		return 0 // stop iteration
	})
	procEnumWindows.Call(cb, 0)
	if found == 0 {
		return 0, fmt.Errorf("no visible top-level window found for pid %d", pid)
	}
	return found, nil
}

// Virtual-key codes and Win32 message IDs for the hotkey path.
const (
	wmKeyDown = 0x0100
	wmKeyUp   = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105

	mapVKVKToVSC = 0 // MapVirtualKey: VK → scan code

	VKShift = 0x10
	VKCtrl  = 0x11
	VKAlt   = 0x12
	VKF1    = 0x70
	VKF2    = 0x71
	VKF3    = 0x72
)

// keyLParam encodes WM_KEYDOWN/WM_KEYUP lParam for a press of key `vk`.
// Qt's QShortcut machinery doesn't fire on plain lParam=0 — it expects
// a populated scan code (bits 16-23) and repeat count (bits 0-15).
// `down` is true for keydown (transition state bit 31 = 0), false for
// keyup (bit 31 = 1, and previous-key-state bit 30 = 1).
func keyLParam(vk uint32, down bool) uintptr {
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(vk), mapVKVKToVSC)
	lp := uint32(1) // repeat count
	lp |= (uint32(scan) & 0xFF) << 16
	if !down {
		lp |= 1 << 30 // previous state = down
		lp |= 1 << 31 // transition  = release
	}
	return uintptr(lp)
}

// SendKeyChord posts a modifier+key chord (e.g. Shift+F1) to the
// target window via PostMessage. Doesn't require the window to be
// foreground — Qt apps process WM_KEYDOWN from their message queue
// and route the resulting QKeyEvent through QShortcut handlers, which
// is how Dolphin's save-state hotkeys are wired.
func SendKeyChord(hwnd uintptr, modifier, key uint32) error {
	if hwnd == 0 {
		return fmt.Errorf("SendKeyChord: nil hwnd")
	}
	// Press modifier first so the chord matches Qt's QKeySequence
	// comparison (which checks held modifiers at the moment the key
	// fires).
	procPostMessageW.Call(hwnd, wmKeyDown, uintptr(modifier), keyLParam(modifier, true))
	procPostMessageW.Call(hwnd, wmKeyDown, uintptr(key), keyLParam(key, true))
	procPostMessageW.Call(hwnd, wmKeyUp, uintptr(key), keyLParam(key, false))
	procPostMessageW.Call(hwnd, wmKeyUp, uintptr(modifier), keyLParam(modifier, false))
	return nil
}

// SendKey posts a single key (no modifier) to the target window.
func SendKey(hwnd uintptr, key uint32) error {
	if hwnd == 0 {
		return fmt.Errorf("SendKey: nil hwnd")
	}
	procPostMessageW.Call(hwnd, wmKeyDown, uintptr(key), keyLParam(key, true))
	procPostMessageW.Call(hwnd, wmKeyUp, uintptr(key), keyLParam(key, false))
	return nil
}

// ForegroundWindow brings the given window to the foreground. Some
// apps only accept synthetic input when foreground; PostMessage paths
// usually don't need this but it's available as a fallback.
//
// SetForegroundWindow is restricted by Windows: it works only from the
// foreground process or with specific permissions. To work around, we
// AttachThreadInput to the target's UI thread, which lets us call
// SetForegroundWindow as if we were the same input queue.
func ForegroundWindow(hwnd uintptr) {
	const swShow = 5
	procShowWindow.Call(hwnd, swShow)

	// Try plain SetForegroundWindow first.
	if ret, _, _ := procSetForegroundWindow.Call(hwnd); ret != 0 {
		return
	}

	// Brief Alt tap to break the foreground lock — Windows allows
	// SetForegroundWindow from a process that owns the most recent
	// keyboard input. Sending Alt down + up via SendInput makes us
	// that process for an instant.
	altDown := []keybdInput{{Type: inputKeyboard, VK: VKAlt, Flags: keyEventKeyDown}}
	altUp := []keybdInput{{Type: inputKeyboard, VK: VKAlt, Flags: keyEventKeyUp}}
	stride := uintptr(unsafe.Sizeof(keybdInput{}))
	procSendInput.Call(1, uintptr(unsafe.Pointer(&altDown[0])), stride)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&altUp[0])), stride)
	if ret, _, _ := procSetForegroundWindow.Call(hwnd); ret != 0 {
		return
	}

	// Last resort: attach to the target's input thread so we share
	// its foreground state.
	targetThread, _, _ := procGetWindowThreadProcessID.Call(hwnd, 0)
	ourThread, _, _ := procGetCurrentThreadId.Call()
	if targetThread != 0 && targetThread != ourThread {
		procAttachThreadInput.Call(ourThread, targetThread, 1)
		procSetForegroundWindow.Call(hwnd)
		procAttachThreadInput.Call(ourThread, targetThread, 0)
	}
}

// keyboardInput / keybdInput mirror Win32's INPUT struct (type=1 keyboard).
// Layout: type(4) + padding(4) + KEYBDINPUT(24).
type keybdInput struct {
	Type    uint32
	_       uint32 // padding on 64-bit
	VK      uint16
	Scan    uint16
	Flags   uint32
	Time    uint32
	ExtraInfo uintptr
	_       [8]byte // pad to MOUSEINPUT max-size union
}

const (
	inputKeyboard    = 1
	keyEventKeyDown  = 0x0000
	keyEventKeyUp    = 0x0002
	keyEventScancode = 0x0008
)

// SendChordToFocusedWindow injects modifier+key into the SYSTEM input
// queue via SendInput. The currently-focused window receives it, which
// must be `hwnd` (or a descendant) for the chord to land on Dolphin.
// Caller is responsible for foregrounding `hwnd` first.
//
// SendInput differs from PostMessage in that it goes through the
// low-level keyboard hook chain and the OS input subsystem, which is
// what Qt apps consume for QKeySequence shortcuts that PostMessage
// can't reach. Includes scan codes (via MapVirtualKey) and dispatches
// each press/release as its own SendInput call with a small delay,
// because Dolphin's polling loop only samples keys at ~250 Hz — events
// dispatched too tightly can be coalesced and missed.
func SendChordToFocusedWindow(modifier, key uint32) error {
	stride := uintptr(unsafe.Sizeof(keybdInput{}))
	send := func(in keybdInput) error {
		ret, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), stride)
		if ret != 1 {
			return fmt.Errorf("SendInput returned %d", ret)
		}
		time.Sleep(50 * time.Millisecond)
		return nil
	}
	modScan, _, _ := procMapVirtualKeyW.Call(uintptr(modifier), mapVKVKToVSC)
	keyScan, _, _ := procMapVirtualKeyW.Call(uintptr(key), mapVKVKToVSC)
	// KEYEVENTF_SCANCODE = 0x0008 — tells SendInput to ignore VK and use
	// the hardware scan code as if from a real keyboard. Some apps that
	// filter LLKHF_INJECTED also check for missing scan codes; supplying
	// a valid one minimises that surface.
	events := []keybdInput{
		{Type: inputKeyboard, Scan: uint16(modScan), Flags: keyEventKeyDown | keyEventScancode},
		{Type: inputKeyboard, Scan: uint16(keyScan), Flags: keyEventKeyDown | keyEventScancode},
		{Type: inputKeyboard, Scan: uint16(keyScan), Flags: keyEventKeyUp | keyEventScancode},
		{Type: inputKeyboard, Scan: uint16(modScan), Flags: keyEventKeyUp | keyEventScancode},
	}
	for _, ev := range events {
		if err := send(ev); err != nil {
			return err
		}
	}
	return nil
}

// GetFocusedWindow returns the hwnd of the currently focused window
// (system-wide). Diagnostic helper for verifying which Qt child has
// keyboard focus after ForegroundWindow.
func GetFocusedWindow() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return hwnd
}

// WindowTitle returns the window title for diagnostic logging.
func WindowTitle(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

// CaptureWindow grabs the pixels of the given HWND using PrintWindow
// with PW_RENDERFULLCONTENT and returns them as a top-down *image.RGBA
// (so png.Encode writes the image right-side-up).
func CaptureWindow(hwnd uintptr) (*image.RGBA, error) {
	var r rect
	if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret == 0 {
		return nil, fmt.Errorf("GetWindowRect failed")
	}
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("window has zero area (%dx%d)", w, h)
	}

	// Get the source window's DC so the memory DC is compatible with
	// the right display configuration (matters on multi-monitor setups).
	srcDC, _, _ := procGetDC.Call(hwnd)
	if srcDC == 0 {
		return nil, fmt.Errorf("GetDC(hwnd) failed")
	}
	defer procReleaseDC.Call(hwnd, srcDC)

	memDC, _, _ := procCreateCompatibleDC.Call(srcDC)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	// Negative height = top-down DIB. PrintWindow + a top-down DIB gives
	// us pixels in the order Go's image/png expects, so no row-flip pass.
	bi := bitmapInfo{
		Header: bitmapInfoHeader{
			Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width:       int32(w),
			Height:      -int32(h),
			Planes:      1,
			BitCount:    32,
			Compression: biRGB,
		},
	}
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSection.Call(
		memDC,
		uintptr(unsafe.Pointer(&bi)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if hbm == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection failed")
	}
	defer procDeleteObject.Call(hbm)

	old, _, _ := procSelectObject.Call(memDC, hbm)
	defer procSelectObject.Call(memDC, old)

	ret, _, _ := procPrintWindow.Call(hwnd, memDC, pwRenderFullContent)
	if ret == 0 {
		return nil, fmt.Errorf("PrintWindow failed (window may be using a renderer that doesn't honor PW_RENDERFULLCONTENT)")
	}

	// DIB is BGRA bottom-up by default, but our negative height made it
	// top-down; pixels are BGRA in memory. Re-pack into image.RGBA.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	src := unsafe.Slice((*byte)(bits), w*h*4)
	for i := 0; i < w*h; i++ {
		b := src[i*4+0]
		g := src[i*4+1]
		r := src[i*4+2]
		// alpha is undefined for PrintWindow output — force opaque
		img.Pix[i*4+0] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = 0xFF
	}
	return img, nil
}
