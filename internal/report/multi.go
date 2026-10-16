package report

// Multi returns a Reporter that hands every line to each non-nil r, in
// order. The session controller uses it to send one session's lines to its
// UI and to an optional log file at once.
func Multi(rs ...Reporter) Reporter {
	m := make(multi, 0, len(rs))
	for _, r := range rs {
		if r != nil {
			m = append(m, r)
		}
	}
	switch len(m) {
	case 0:
		return Discard{}
	case 1:
		return m[0]
	}
	return m
}

type multi []Reporter

func (m multi) Log(level Level, msg string) {
	for _, r := range m {
		r.Log(level, msg)
	}
}
