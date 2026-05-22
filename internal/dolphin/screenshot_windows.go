//go:build windows

package dolphin

import (
	"fmt"
	"image"
	"syscall"
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
