package lockfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/CDFalcon/ccmux/internal/logging"
)

// ReadJSON reads a JSON state file, recovering from the one corruption shape
// ccmux is known to have produced.
//
// Before every store wrote through WriteFileAtomic, two processes could
// truncate-and-write the same file at once. When the longer write landed first
// and the shorter one followed, the result was the shorter (newer) document
// followed by the tail of the older one — agents.json ending in "}}", or a
// queue.json whose first document is followed by half of the previous one.
// Such a file stays on disk forever: every subsequent load fails with
// "invalid character ... after top-level value", every Stop hook in every
// session reports it, and no agent's status transition ever runs again, until a
// human notices and repairs the file by hand.
//
// A leading complete document is by construction the most recent full write,
// so ReadJSON returns exactly that document and ignores the trailing bytes. For
// forensics it keeps one copy of the raw file beside it, named by content hash
// so a polling reader (the TUI loads these files every tick) creates at most one
// quarantine file per distinct corruption. The next save through
// WriteFileAtomic replaces the file with clean content.
//
// A file with no leading complete document (a truncated write) is reported as a
// parse error that names the path; ReadJSON never guesses at content.
//
// A missing file is reported with the error from os.ReadFile, so callers keep
// using os.IsNotExist to treat it as an empty store.
func ReadJSON(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if json.Valid(raw) {
		return raw, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	doc := raw[:dec.InputOffset()]
	if len(bytes.TrimSpace(raw[dec.InputOffset():])) == 0 {
		// Only whitespace followed the value; json.Valid would have accepted
		// this, so it cannot happen — but never return more than we checked.
		return doc, nil
	}

	quarantine(path, raw)
	return doc, nil
}

// quarantine keeps one copy of a corrupt file's raw bytes beside it. Best
// effort: a failure to write the copy must not turn a recoverable load into an
// error.
func quarantine(path string, raw []byte) {
	sum := sha256.Sum256(raw)
	backup := fmt.Sprintf("%s.corrupt-%s", path, hex.EncodeToString(sum[:4]))
	if _, err := os.Stat(backup); err == nil {
		return
	}
	if err := WriteFileAtomic(backup, raw, 0644); err != nil {
		logging.Log("ReadJSON: %s has trailing garbage after its first document; recovered the document but could not save a copy of the corrupt file: %v", path, err)
		return
	}
	logging.Log("ReadJSON: %s has trailing garbage after its first document (a pre-lockfile concurrent write); recovered the document and saved the corrupt file as %s", path, backup)
}
