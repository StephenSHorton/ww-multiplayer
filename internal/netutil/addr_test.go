package netutil

import "testing"

func TestNormalizeAddr(t *testing.T) {
	const defaultPort = 25565

	cases := []struct {
		in   string
		want string
	}{
		{"1.2.3.4", "1.2.3.4:25565"},
		{"1.2.3.4:1234", "1.2.3.4:1234"},
		{"myhost", "myhost:25565"},
		{"myhost:1234", "myhost:1234"},
		{"[::1]", "[::1]:25565"},
		{"[::1]:1234", "[::1]:1234"},
		{"::1", "[::1]:25565"},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := NormalizeAddr(c.in, defaultPort)
			if got != c.want {
				t.Errorf("NormalizeAddr(%q, %d) = %q, want %q", c.in, defaultPort, got, c.want)
			}
		})
	}
}
