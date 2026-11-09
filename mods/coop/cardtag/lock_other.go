//go:build !unix && !windows

package cardtag

// tryLock has no lock between processes here: the app runs on Windows,
// macOS and Linux.
func tryLock(string) (unlock func(), busy bool, err error) { return func() {}, false, nil }
