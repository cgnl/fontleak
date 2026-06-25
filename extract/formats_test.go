package extract

import (
	"bytes"
	"os"
	"testing"

	gtfont "github.com/go-text/typesetting/font"
)

// Each container format must decode to a parseable sfnt that exposes the
// font's GSUB lookups (so the rest of the pipeline can solve it).
func TestFormatsDecodeToWorkingFont(t *testing.T) {
	cases := []struct {
		path    string
		minGSUB int
	}{
		{"../../fmt_fixtures/themepark.woff", 300},
		{"../../fmt_fixtures/themepark.woff2", 300},
		{"../../fmt_fixtures/themepark.css", 300},
		{"../../fmt_fixtures/themepark.pdf", 300},
	}
	for _, c := range cases {
		if _, err := os.Stat(c.path); err != nil {
			t.Skipf("fixture %s missing", c.path)
		}
		fonts, err := FromFile(c.path)
		if err != nil {
			t.Errorf("%s: FromFile: %v", c.path, err)
			continue
		}
		if len(fonts) == 0 || len(fonts[0].Data) == 0 {
			t.Errorf("%s: no font recovered", c.path)
			continue
		}
		face, err := gtfont.ParseTTF(bytes.NewReader(fonts[0].Data))
		if err != nil {
			t.Errorf("%s: parse recovered font: %v", c.path, err)
			continue
		}
		if n := len(face.Font.GSUB.Lookups); n < c.minGSUB {
			t.Errorf("%s: GSUB lookups %d < %d", c.path, n, c.minGSUB)
		} else {
			t.Logf("%s: ok, %d GSUB lookups", c.path, n)
		}
	}
}
