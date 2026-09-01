package atomicfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var errSentinel = errors.New("sentinel I/O failure")

type payload struct {
	Name string `json:"name"`
}

const tmpPattern = ".atomicfile-*.tmp"

func write(t *testing.T, path string) error {
	t.Helper()
	return WriteJSON(path, tmpPattern, "", payload{Name: "v"})
}

// resetHooks restores every injectable syscall after the test, so a stub
// cannot leak into the next one.
func resetHooks(t *testing.T) {
	t.Helper()
	mkdirAll, chmod, rename, remove, sink := osMkdirAll, osChmod, osRename, osRemove, newSink
	t.Cleanup(func() {
		osMkdirAll, osChmod, osRename, osRemove, newSink = mkdirAll, chmod, rename, remove, sink
	})
}

func TestWriteJSONRoundTripsThroughParentDirsAtSharedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "out.json")
	if err := write(t, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got payload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "v" {
		t.Fatalf("payload=%+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != Mode {
		// CreateTemp is owner-only; without the chmod both artifacts land at
		// 0600 and a later step running as another account cannot read them.
		t.Fatalf("permissions=%#o, want %#o", info.Mode().Perm(), Mode)
	}
}

func TestWriteJSONIndentsWhenAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	if err := WriteJSON(path, tmpPattern, "  ", payload{Name: "v"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\n  \"name\": \"v\"\n}\n" {
		t.Fatalf("encoded=%q, want two-space indentation", data)
	}
}

func TestWriteJSONLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	if err := write(t, filepath.Join(dir, "out.json")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "out.json" {
		t.Fatalf("directory contents=%v, want only the renamed target", entries)
	}
}

// fakeSink is a Sink whose Write and Close errors can be set independently —
// what lets the Encode-only and Close-only cases below distinguish those
// return paths (otherwise indistinguishable when both errors collapse into a
// single "non-nil error" assertion).
type fakeSink struct {
	name     string
	writeErr error
	closeErr error
	closed   bool
}

func (f *fakeSink) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}
func (f *fakeSink) Close() error { f.closed = true; return f.closeErr }
func (f *fakeSink) Name() string { return f.name }

// TestWriteJSONPropagatesEveryFailure walks each syscall and sink error in
// turn. Encode and Close carry their own sentinels rather than the shared one:
// asserting "any non-nil error" would let either branch stand in for the
// other, and Close runs unconditionally so both can fire in one call.
func TestWriteJSONPropagatesEveryFailure(t *testing.T) {
	encErr := errors.New("encode-only sentinel")
	closeErr := errors.New("close-only sentinel")
	cases := []struct {
		name string
		fail func(t *testing.T, tmpPath string)
		want error
	}{
		{
			name: "mkdir",
			fail: func(_ *testing.T, _ string) {
				osMkdirAll = func(string, os.FileMode) error { return errSentinel }
			},
			want: errSentinel,
		},
		{
			name: "create temp",
			fail: func(_ *testing.T, _ string) {
				newSink = func(string, string) (Sink, error) { return nil, errSentinel }
			},
			want: errSentinel,
		},
		{
			name: "encode, with close succeeding",
			fail: func(_ *testing.T, tmpPath string) {
				newSink = func(string, string) (Sink, error) {
					return &fakeSink{name: tmpPath, writeErr: encErr}, nil
				}
			},
			want: encErr,
		},
		{
			name: "close, with encode succeeding",
			fail: func(_ *testing.T, tmpPath string) {
				newSink = func(string, string) (Sink, error) {
					return &fakeSink{name: tmpPath, closeErr: closeErr}, nil
				}
			},
			want: closeErr,
		},
		{
			name: "chmod",
			fail: func(_ *testing.T, _ string) {
				osChmod = func(string, os.FileMode) error { return errSentinel }
			},
			want: errSentinel,
		},
		{
			name: "rename",
			fail: func(_ *testing.T, _ string) {
				osRename = func(string, string) error { return errSentinel }
			},
			want: errSentinel,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetHooks(t)
			dir := t.TempDir()
			tc.fail(t, filepath.Join(dir, ".atomicfile-fake.tmp"))
			if err := write(t, filepath.Join(dir, "out.json")); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
}

// TestWriteJSONAlwaysClosesEvenOnEncodeFailure asserts the file descriptor is
// released regardless of Encode outcome — i.e. Close is invoked on every code
// path, not gated on Encode success.
func TestWriteJSONAlwaysClosesEvenOnEncodeFailure(t *testing.T) {
	resetHooks(t)
	dir := t.TempDir()
	sink := &fakeSink{name: filepath.Join(dir, ".atomicfile-fake.tmp"), writeErr: errSentinel}
	newSink = func(string, string) (Sink, error) { return sink, nil }

	_ = write(t, filepath.Join(dir, "out.json"))
	if !sink.closed {
		t.Fatal("WriteJSON did not Close the sink on encode failure")
	}
}

func TestWriteJSONLeavesNoTempFileAfterRenameFailure(t *testing.T) {
	resetHooks(t)
	dir := t.TempDir()
	osRename = func(string, string) error { return errSentinel }
	if err := write(t, filepath.Join(dir, "out.json")); !errors.Is(err, errSentinel) {
		t.Fatalf("err=%v, want sentinel", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temp file leaked after rename failure: %v", entries)
	}
}

// TestWriteJSONRemovesTmpOnlyOnFailurePath asserts the `committed` flag: the
// deferred Remove must fire on the failure path and must NOT fire on the
// success path. With `committed = true` removed, defer would call
// osRemove(tmpName) after the successful rename, and the stub counts it.
func TestWriteJSONRemovesTmpOnlyOnFailurePath(t *testing.T) {
	resetHooks(t)
	var calls int
	osRemove = func(name string) error {
		calls++
		return os.Remove(name)
	}

	t.Run("success path: no Remove", func(t *testing.T) {
		calls = 0
		if err := write(t, filepath.Join(t.TempDir(), "out.json")); err != nil {
			t.Fatal(err)
		}
		if calls != 0 {
			t.Errorf("osRemove called %d times on success path, want 0 — committed flag broken", calls)
		}
	})

	t.Run("failure path: one Remove", func(t *testing.T) {
		resetHooks(t)
		calls = 0
		osRename = func(string, string) error { return errSentinel }
		if err := write(t, filepath.Join(t.TempDir(), "out.json")); !errors.Is(err, errSentinel) {
			t.Fatalf("expected sentinel, got %v", err)
		}
		if calls != 1 {
			t.Errorf("osRemove called %d times on failure path, want 1 — cleanup did not fire", calls)
		}
	})

	// A failed Encode must trigger the same deferred cleanup.
	t.Run("encode failure: one Remove", func(t *testing.T) {
		resetHooks(t)
		calls = 0
		dir := t.TempDir()
		newSink = func(string, string) (Sink, error) {
			return &fakeSink{name: filepath.Join(dir, ".atomicfile-fake.tmp"), writeErr: errSentinel}, nil
		}
		if err := write(t, filepath.Join(dir, "out.json")); err == nil {
			t.Fatal("expected error, got nil")
		}
		if calls != 1 {
			t.Errorf("osRemove calls=%d, want 1 (deferred cleanup must fire on encode failure)", calls)
		}
	})
}
