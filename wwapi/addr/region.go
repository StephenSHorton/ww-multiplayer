package addr

// Region is a half-open range [Start, End) of MEM1.
type Region struct {
	Name       string
	Start, End uint32
}

// Size is End - Start.
func (r Region) Size() uint32 { return r.End - r.Start }

// Contains reports whether [a, a+n) lies inside r.
func (r Region) Contains(a uint32, n int) bool {
	return n >= 0 && a >= r.Start && uint64(a)+uint64(n) <= uint64(r.End)
}
