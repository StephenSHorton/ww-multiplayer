package log

// Prefix returns a Reporter that prepends a static label and a space to
// every line before handing it to r. mp-local uses it to tag each
// goroutine's output (`[server]`, `[bcast-A]`, etc.) so the merged stdout
// stream is readable.
func Prefix(r Reporter, prefix string) Reporter {
	return prefixReporter{inner: r, prefix: prefix}
}

type prefixReporter struct {
	inner  Reporter
	prefix string
}

func (p prefixReporter) Log(level Level, msg string) {
	p.inner.Log(level, p.prefix+" "+msg)
}
