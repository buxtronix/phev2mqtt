package cmd

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// logRing is a logrus hook that keeps the last log lines for the web
// interface. It sees what the log level lets through, nothing more.
type logRing struct {
	mu      sync.Mutex
	lines   []logLine
	size    int
	next    int64
	changed chan struct{}
}

type logLine struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

func newLogRing(size int) *logRing {
	return &logRing{size: size, next: 1, changed: make(chan struct{})}
}

func (r *logRing) Levels() []log.Level { return log.AllLevels }

// Fire must not log itself: logrus calls it while logging.
func (r *logRing) Fire(e *log.Entry) error {
	msg := e.Message
	if len(e.Data) > 0 {
		keys := make([]string, 0, len(e.Data))
		for k := range e.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(msg)
		for _, k := range keys {
			fmt.Fprintf(&b, " %s=%v", k, e.Data[k])
		}
		msg = b.String()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, logLine{Seq: r.next, Time: e.Time, Level: e.Level.String(), Message: msg})
	r.next++
	if len(r.lines) > r.size {
		r.lines = append(r.lines[:0], r.lines[len(r.lines)-r.size:]...)
	}
	close(r.changed)
	r.changed = make(chan struct{})
	return nil
}

// since returns the lines after seq.
func (r *logRing) since(seq int64) []logLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := sort.Search(len(r.lines), func(i int) bool { return r.lines[i].Seq > seq })
	return append([]logLine(nil), r.lines[i:]...)
}

// changes returns a channel that is closed at the next log line.
func (r *logRing) changes() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}
