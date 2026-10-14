//go:build unix

package inject

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A new output gets 0644 minus the umask, as os.WriteFile would give it,
// not a fixed mode.
func TestPatchISOFileNewOutputHonorsUmask(t *testing.T) {
	for _, c := range []struct{ umask, want os.FileMode }{{0o077, 0o600}, {0o027, 0o640}} {
		func() {
			defer syscall.Umask(syscall.Umask(int(c.umask)))
			b := vanillaLike().build(t)
			dir := t.TempDir()
			in := filepath.Join(dir, "ww.iso")
			out := filepath.Join(dir, "ww-multiplayer.iso")
			writeFile(t, in, b.img)

			if _, err := PatchISO(in, out); err != nil {
				t.Fatal(err)
			}
			st, err := os.Stat(out)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != c.want {
				t.Errorf("output mode = %v under umask %03o, want %v", st.Mode().Perm(), c.umask, c.want)
			}
		}()
	}
}
