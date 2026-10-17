package avatar

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// TestNextSeqSkipsZero: the C side treats a pose/face seq of 0 as "nothing
// written yet", so a full cycle of bumps must never produce 0, and must
// visit every other value once before repeating.
func TestNextSeqSkipsZero(t *testing.T) {
	cases := []struct{ cur, want byte }{
		{0, 1}, // cold mailbox: first write is 1
		{1, 2},
		{0xFE, 0xFF},
		{0xFF, 1}, // wrap skips 0
	}
	for _, c := range cases {
		if got := NextSeq(c.cur); got != c.want {
			t.Errorf("NextSeq(%d) = %d, want %d", c.cur, got, c.want)
		}
	}

	seen := map[byte]bool{}
	seq := byte(0)
	for i := 0; i < 255; i++ {
		seq = NextSeq(seq)
		if seq == 0 {
			t.Fatalf("bump %d produced 0", i+1)
		}
		if seen[seq] {
			t.Fatalf("bump %d repeated %d before the cycle ended", i+1, seq)
		}
		seen[seq] = true
	}
	if seq = NextSeq(seq); seq != 1 {
		t.Fatalf("after a full cycle NextSeq = %d, want 1", seq)
	}
}

func TestLinkPoseUsable(t *testing.T) {
	const full = JointCount * MtxBytes
	cases := []struct {
		name          string
		joints, bytes int
		want          bool
	}{
		{"link", JointCount, full, true},
		{"no pose", 0, 0, false},
		{"fewer joints", 41, 41 * MtxBytes, false},
		{"more joints", 128, 128 * MtxBytes, false},
		{"short buffer", JointCount, full - 1, false},
		{"long buffer", JointCount, full + MtxBytes, false},
	}
	for _, c := range cases {
		if got := PoseUsable(c.joints, c.bytes); got != c.want {
			t.Errorf("%s: PoseUsable(%d, %d) = %v, want %v", c.name, c.joints, c.bytes, got, c.want)
		}
	}
}

func TestPoseDstInMEM1(t *testing.T) {
	const n = JointCount * MtxBytes
	cases := []struct {
		name string
		dst  uint32
		n    int
		want bool
	}{
		{"heap pointer", 0x8150_0000, n, true},
		{"start of MEM1", addr.MEM1Base, n, true},
		{"ends exactly at MEM1 end", addr.MEM1End - n, n, true},
		{"ends one byte past MEM1", addr.MEM1End - n + 1, n, false},
		{"null", 0, n, false},
		{"below MEM1", 0x7FFF_FFF0, n, false},
		{"MEM1 end", addr.MEM1End, n, false},
		{"uncached mirror", 0xC000_0000, n, false},
		{"wraps past 2^32", 0xFFFF_FFF0, n, false},
		{"negative length", 0x8150_0000, -1, false},
	}
	for _, c := range cases {
		if got := PoseDstInMEM1(c.dst, c.n); got != c.want {
			t.Errorf("%s: PoseDstInMEM1(0x%08X, %d) = %v, want %v", c.name, c.dst, c.n, got, c.want)
		}
	}
}
