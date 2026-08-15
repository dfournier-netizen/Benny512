package capture

import (
	"fmt"
	"os"
	"sync"
)

// DefaultDiskLogMaxBytes is the size threshold at which DiskLogger rotates
// (report task: "rotate or cap sensibly" — a bench session's RDM traffic is
// small per-message, so 25MB is generous headroom for a long day without
// growing unbounded).
const DefaultDiskLogMaxBytes = 25 * 1024 * 1024

// DefaultDiskLogMaxBackups is how many rotated files DiskLogger keeps
// alongside the active one (oldest deleted first).
const DefaultDiskLogMaxBackups = 3

// DiskLogger appends RDM/ToD exchanges to a file as they happen (report
// task: "so a long bench session isn't limited by the in-memory ring
// buffer"), one FormatEntryText block per entry, rotating by size so the
// file never grows without bound across a multi-hour session.
type DiskLogger struct {
	path       string
	maxBytes   int64
	maxBackups int
	mu         sync.Mutex
	f          *os.File
	written    int64
	closed     bool
}

// OpenDiskLogger opens (creating/appending) path for continuous RDM
// logging. maxBytes/maxBackups <= 0 fall back to the package defaults.
func OpenDiskLogger(path string, maxBytes int64, maxBackups int) (*DiskLogger, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultDiskLogMaxBytes
	}
	if maxBackups <= 0 {
		maxBackups = DefaultDiskLogMaxBackups
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("capture: open RDM log %q: %w", path, err)
	}
	info, err := f.Stat()
	written := int64(0)
	if err == nil {
		written = info.Size()
	}
	return &DiskLogger{path: path, maxBytes: maxBytes, maxBackups: maxBackups, f: f, written: written}, nil
}

// Path returns the log file path this logger writes to.
func (l *DiskLogger) Path() string {
	return l.path
}

// Log appends one entry's formatted text block, rotating first if the file
// has grown past the size cap. Errors are swallowed (best-effort logging
// must never take down live RDM traffic handling) but the most recent one
// is retained for LastError.
func (l *DiskLogger) Log(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if l.written >= l.maxBytes {
		l.rotateLocked()
	}
	text := FormatEntryText(e)
	n, err := l.f.WriteString(text + "\n")
	if err == nil {
		l.written += int64(n)
	}
}

func (l *DiskLogger) rotateLocked() {
	_ = l.f.Close()
	for i := l.maxBackups - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", l.path, i)
		next := fmt.Sprintf("%s.%d", l.path, i+1)
		_ = os.Rename(old, next)
	}
	_ = os.Rename(l.path, fmt.Sprintf("%s.1", l.path))
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		// Best-effort: fall back to appending to whatever's left rather than
		// losing the log stream entirely.
		f, _ = os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	}
	l.f = f
	l.written = 0
}

// Close flushes and closes the underlying file.
func (l *DiskLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.f.Close()
}
