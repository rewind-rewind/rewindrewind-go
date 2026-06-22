package rewindrewind

import (
	"go/build"
	"runtime"
	"strings"
)

// maxFrames bounds the number of stack frames captured per exception. Deep
// recursion or framework plumbing rarely adds value beyond this.
const maxFrames = 64

// Frame is a single stack frame in the format the RewindRewind server reads.
// The server derives an issue's "culprit" from the first frame with InApp ==
// true (falling back to the last frame), so accurate InApp detection is the
// single most important fidelity property of this package.
type Frame struct {
	Filename string `json:"filename"`
	Function string `json:"function"`
	Module   string `json:"module,omitempty"`
	Line     int    `json:"line"`
	Column   int    `json:"column,omitempty"`
	InApp    bool   `json:"in_app"`
}

// goRoot and goPath are resolved once. We prefer the values the runtime/build
// package reports for the running binary; they reflect the actual toolchain.
var (
	goRoot = strings.TrimRight(build.Default.GOROOT, "/")
	goPath = strings.TrimRight(build.Default.GOPATH, "/")
)

// modCacheMarkers identify dependency code living in the module cache. Any frame
// whose file path contains one of these is third-party and not "in app".
var modCacheMarkers = []string{"/go/pkg/mod/", "/pkg/mod/"}

// trimFilePath converts an absolute build-machine path into a short, portable
// path suitable for shipping off-box. It strips, in order of preference:
//
//   - the Go module cache prefix, leaving "module@version/rel/path.go";
//   - the GOROOT prefix, leaving "src/..." for standard-library frames;
//   - the GOPATH/src prefix, leaving the import-path-relative file;
//
// and otherwise falls back to the file's base name. Relative paths and empty
// inputs are returned unchanged.
func trimFilePath(file string) string {
	if file == "" {
		return ""
	}

	// Dependency code in the module cache: keep "module@version/..." which is
	// already non-sensitive and useful, dropping the absolute prefix.
	for _, marker := range modCacheMarkers {
		if i := strings.LastIndex(file, marker); i >= 0 {
			return file[i+len(marker):]
		}
	}

	// Standard library / toolchain under GOROOT: keep the "src/..." tail.
	if goRoot != "" && strings.HasPrefix(file, goRoot+"/") {
		rel := file[len(goRoot)+1:]
		return rel
	}

	// GOPATH/src layout: keep the import-path-relative file.
	if goPath != "" {
		srcPrefix := goPath + "/src/"
		if strings.HasPrefix(file, srcPrefix) {
			return file[len(srcPrefix):]
		}
	}

	// First-party code: trim to the detected module root so the path is
	// repo-relative (e.g. "internal/checkout/charge.go").
	if moduleRoot != "" && strings.HasPrefix(file, moduleRoot+"/") {
		return file[len(moduleRoot)+1:]
	}

	// Unknown layout: fall back to the base name, which leaks no directory
	// structure but still identifies the file.
	if i := strings.LastIndexByte(file, '/'); i >= 0 {
		return file[i+1:]
	}
	return file
}

// moduleRoot is the absolute filesystem directory of the consuming application's
// module root, best-effort detected at init from the runtime location of a
// non-SDK caller. It lets trimFilePath produce repo-relative paths for
// first-party frames. Empty when it cannot be determined.
var moduleRoot = detectModuleRoot()

// detectModuleRoot infers the build-machine module/repo root from this SDK
// source file's own absolute path. The SDK lives at "<root>/sdk/go/...", but we
// can't assume that layout for arbitrary consumers; instead we walk up from this
// file's directory until the path no longer looks like it's inside the SDK
// package, which yields a usable common prefix to trim. A failure returns "".
func detectModuleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok || file == "" {
		return ""
	}
	// file is ".../<root>/sdk/go/stacktrace.go". Trim the known SDK suffix when
	// present so first-party app paths sharing the repo root become relative.
	dir := file
	for _, suffix := range []string{"/stacktrace.go", "/sdk/go", "/sdk"} {
		if strings.HasSuffix(dir, suffix) {
			dir = dir[:len(dir)-len(suffix)]
		}
	}
	return dir
}

// captureStack walks the call stack starting `skip` frames above the caller of
// captureStack (skip is relative to captureStack's own caller), returning frames
// ordered from the innermost (where the error surfaced) outward. SDK frames are
// always elided so the culprit lands on application code.
//
// Frames are returned innermost-first, which matches how the server scans for
// the first in_app frame to choose a culprit.
func captureStack(skip int) []Frame {
	// +2: skip runtime.Callers itself and captureStack's own frame, then the
	// caller-supplied skip lands on the first frame the caller cares about.
	var pcs [maxFrames]uintptr
	n := runtime.Callers(skip+2, pcs[:])
	if n == 0 {
		return nil
	}

	callerFrames := runtime.CallersFrames(pcs[:n])
	frames := make([]Frame, 0, n)
	for {
		f, more := callerFrames.Next()
		if f.Function == "" && f.File == "" {
			if !more {
				break
			}
			continue
		}
		// Drop this SDK's own frames: they are never a meaningful culprit and
		// would otherwise mask the real application frame.
		if isSDKFrame(f.Function) {
			if !more {
				break
			}
			continue
		}

		pkg, fn := splitPackageFunc(f.Function)
		frames = append(frames, Frame{
			// in_app is computed from the original absolute path (it depends on
			// GOROOT / module-cache prefixes); the reported filename is then
			// trimmed so we never leak the build-machine's absolute layout.
			Filename: trimFilePath(f.File),
			Function: fn,
			Module:   pkg,
			Line:     f.Line,
			InApp:    isInApp(f.File, f.Function),
		})
		if !more {
			break
		}
	}
	return frames
}

// isInApp reports whether a frame belongs to first-party application code. A
// frame is in-app when it is NOT part of the Go standard library (under GOROOT)
// and NOT a downloaded dependency (under a module cache path). Runtime and SDK
// frames are excluded as well.
func isInApp(file, function string) bool {
	if file == "" {
		return false
	}
	// Standard library / runtime: under GOROOT, or a synthetic runtime frame
	// with no real file path.
	if goRoot != "" && strings.HasPrefix(file, goRoot+"/") {
		return false
	}
	if strings.HasPrefix(function, "runtime.") || strings.HasPrefix(function, "runtime/") {
		return false
	}
	// Dependencies resolved into the module cache.
	for _, marker := range modCacheMarkers {
		if strings.Contains(file, marker) {
			return false
		}
	}
	// GOPATH-style vendored module cache (GOPATH/pkg/mod) is covered above via
	// the markers, but guard the bare GOPATH/src layout too.
	if goPath != "" && strings.HasPrefix(file, goPath+"/pkg/mod/") {
		return false
	}
	// The SDK itself is not application code.
	if isSDKFrame(function) {
		return false
	}
	return true
}

// sdkPackagePath is this SDK package's import path. Frames belonging to the SDK
// package itself (its capture/middleware machinery) are elided from every
// captured stack so the culprit lands on application code. It is resolved at
// init from a real SDK symbol so it stays correct if the module is renamed or
// vendored under a different path.
var sdkPackagePath = func() string {
	pc, _, _, ok := runtime.Caller(0)
	if !ok {
		return "rewindrewind.com/go"
	}
	if fn := runtime.FuncForPC(pc); fn != nil {
		pkg, _ := splitPackageFunc(fn.Name())
		return pkg
	}
	return "rewindrewind.com/go"
}()

// isSDKFrame reports whether a frame belongs to the SDK package's own internal
// machinery (as opposed to application code, even in the same module). It
// matches the exact package path, not a prefix, so a consumer's sibling
// packages are never elided.
func isSDKFrame(function string) bool {
	pkg, _ := splitPackageFunc(function)
	return pkg == sdkPackagePath
}

// splitPackageFunc splits a runtime function symbol such as
// "github.com/acme/app/pkg.(*T).Method" into its package import path
// ("github.com/acme/app/pkg") and the function/method portion ("(*T).Method").
func splitPackageFunc(symbol string) (pkg, fn string) {
	if symbol == "" {
		return "", ""
	}
	// The package path is everything up to the last "/" segment's first ".".
	// e.g. "a/b/c.Foo" -> pkg "a/b/c", fn "Foo".
	lastSlash := strings.LastIndex(symbol, "/")
	dot := strings.IndexByte(symbol[lastSlash+1:], '.')
	if dot < 0 {
		return "", symbol
	}
	dot += lastSlash + 1
	return symbol[:dot], symbol[dot+1:]
}
