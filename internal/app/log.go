package app

import (
	"fmt"
	"os"
	"time"
)

// maxLogSize is when the log is rotated; one old file is kept.
const maxLogSize = 1 << 20

// Logger appends timestamped lines to a file. Logging never fails loudly.
type Logger struct {
	Path string
}

// Printf writes one line.
func (l *Logger) Printf(format string, args ...any) {
	if st, err := os.Stat(l.Path); err == nil && st.Size() > maxLogSize {
		_ = os.Rename(l.Path, l.Path+".1")
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02T15:04:05.000"), fmt.Sprintf(format, args...))
}
