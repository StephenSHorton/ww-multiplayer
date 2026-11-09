//go:build windows

package cardtag

import (
	"errors"
	"syscall"
)

// errorSharingViolation is ERROR_SHARING_VIOLATION: another process has
// the file open.
const errorSharingViolation = syscall.Errno(32)

// tryLock opens path with no sharing, so no other process can open it
// until the handle is closed: busy when another process has it. Windows
// closes the handle with the process.
func tryLock(path string) (unlock func(), busy bool, err error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, errorSharingViolation) || errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() { syscall.CloseHandle(h) }, false, nil
}
