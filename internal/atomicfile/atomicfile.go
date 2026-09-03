// Package atomicfile writes JSON artifacts that must never be observed
// half-written. gomutants persists two such files — the incremental cache and
// the committed baseline — and both are read back by a later run that has no
// way to tell a truncated file from a stale one.
package atomicfile

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// I/O syscalls are exposed as package-level function variables so tests can
// inject failures into each error path independently. Production code calls
// them through these vars; tests swap them out for stubs that return
// controlled errors. Mirrors the `var preReadFilesFunc = ...` pattern in
// main.go.
var (
	osMkdirAll = os.MkdirAll
	osChmod    = os.Chmod
	osRename   = os.Rename
	osRemove   = os.Remove

	// newSink wraps the os.CreateTemp call so WriteJSON's flow runs against
	// an interface (Sink) rather than *os.File directly. This is what lets a
	// test simulate "Encode fails / Close succeeds" or "Encode succeeds /
	// Close fails" — a contrast that can't be produced from a real *os.File
	// without filesystem-specific tricks.
	newSink = func(dir, pattern string) (Sink, error) {
		return os.CreateTemp(dir, pattern)
	}
)

// Sink is the minimal surface WriteJSON needs from a temp-file handle: write
// the encoded JSON, close the descriptor, and report the on-disk path so
// Chmod/Rename/Remove can target it. *os.File satisfies this directly; tests
// substitute a fake to inject controlled errors.
type Sink interface {
	io.Writer
	io.Closer
	Name() string
}

// Mode is the permission the finished file carries. os.CreateTemp makes the
// temp file owner-only, which is wrong for both artifacts: they are ordinary
// project files, read by whichever account the next step or the next developer
// runs as.
const Mode = 0o644

// WriteJSON encodes v as JSON into path, creating parent directories as
// needed. The write is atomic within the target's filesystem: serialization
// goes to a temp file named by tmpPattern in the same directory, then
// os.Rename swaps it into place. A crash before the rename leaves the prior
// file untouched; a crash after leaves the new one fully written. Either way
// the file on disk parses successfully on the next read.
//
// indent is passed to json.Encoder.SetIndent; pass "" for compact output.
func WriteJSON(path, tmpPattern, indent string, v any) error {
	dir := filepath.Dir(path)
	if err := osMkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Temp file must live in the same directory as the target so os.Rename
	// stays atomic — cross-filesystem renames degrade to copy+unlink on some
	// platforms.
	tmp, err := newSink(dir, tmpPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// committed flips to true once the rename has placed the file at `path`;
	// the deferred cleanup removes the *original* tmp path only on the
	// failure path. (Once committed, tmpName no longer exists on disk, so
	// calling Remove on it would be wrong.)
	committed := false
	defer func() {
		if !committed {
			_ = osRemove(tmpName)
		}
	}()

	// Encode + Close happen unconditionally (we always want to release the
	// file descriptor) and the first non-nil error wins. Wiring it this way
	// means an Encode failure that was followed by a successful Close still
	// surfaces — without this, a mutant that drops the encode-error return
	// would silently produce a bogus file.
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", indent)
	// These files are read by people and by diff tools, never embedded in a
	// web page. Go escapes <, > and & by default, which is exactly the set a
	// mutation record is made of: a committed baseline would spell every
	// comparison and logical operator <, > and &.
	enc.SetEscapeHTML(false)
	encodeErr := enc.Encode(v)
	closeErr := tmp.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Widen before the rename so the file is never briefly visible at the
	// wrong mode under its final name.
	if err := osChmod(tmpName, Mode); err != nil {
		return err
	}
	if err := osRename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}
