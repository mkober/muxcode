package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestAtLeastExit(t *testing.T) {
	cases := []struct {
		have, want string
		code       int
		msg        string
	}{
		{"v0.2.0", "v0.1.0", 0, ""},
		{"v0.1.0", "v0.1.0", 0, ""},
		{"v0.1.0-3-gabc1234", "v0.1.0", 0, ""},
		{"v0.1.0", "v0.2.0", 1, "older than required"},
		{"v1.0.0-rc1", "v1.0.0", 1, "older than required"},
		{"2f55e13-dirty", "v0.1.0", 2, "cannot compare"},
		{"devel", "v0.1.0", 2, "cannot compare"},
		{"v0.1.0", "latest", 2, "cannot compare"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if got := atLeastExit(c.have, c.want, &buf); got != c.code {
			t.Errorf("atLeastExit(%q, %q) = %d, want %d (stderr %q)", c.have, c.want, got, c.code, buf.String())
		}
		if c.msg == "" && buf.Len() != 0 {
			t.Errorf("atLeastExit(%q, %q) wrote %q on success", c.have, c.want, buf.String())
		}
		if c.msg != "" && !strings.Contains(buf.String(), c.msg) {
			t.Errorf("atLeastExit(%q, %q) stderr %q, want %q", c.have, c.want, buf.String(), c.msg)
		}
	}
}
