package rewindrewind

import "testing"

// White-box tests for in_app classification, which depends on unexported helpers.

func TestRuntimeFrameNotInApp(t *testing.T) {
	if isInApp("/usr/local/go/src/runtime/panic.go", "runtime.gopanic") {
		t.Error("runtime frame should not be in_app")
	}
	if isInApp("/home/u/go/pkg/mod/github.com/foo/bar@v1.0.0/x.go", "github.com/foo/bar.F") {
		t.Error("module-cache frame should not be in_app")
	}
	if !isInApp("/home/u/project/main.go", "main.main") {
		t.Error("first-party frame should be in_app")
	}
}

func TestSplitPackageFunc(t *testing.T) {
	cases := []struct{ in, pkg, fn string }{
		{"main.main", "main", "main"},
		{"plainfunc", "", "plainfunc"},
		{"github.com/acme/app.Run", "github.com/acme/app", "Run"},
		{"github.com/acme/app/pkg.(*T).Method", "github.com/acme/app/pkg", "(*T).Method"},
	}
	for _, c := range cases {
		pkg, fn := splitPackageFunc(c.in)
		if pkg != c.pkg || fn != c.fn {
			t.Errorf("splitPackageFunc(%q) = (%q,%q), want (%q,%q)", c.in, pkg, fn, c.pkg, c.fn)
		}
	}
}
