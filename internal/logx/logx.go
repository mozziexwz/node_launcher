// Package logx is the launcher's logger: tagged lines, one file per day with
// a retention window, secrets masked before anything is stored, and a small
// in-memory history plus live subscriptions for the UI.
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tag names the component a line comes from. Component names stay generic so
// the UI never shows upstream project names.
type Tag string

const (
	App      Tag = "程序"
	Core     Tag = "核心"
	Redirect Tag = "分流"
	Conn     Tag = "连接" // connection records of the accelerated programs
)

// Line is one log entry. Seq increases by one per line, so readers can merge
// Recent with Subscribe without duplicates.
type Line struct {
	Seq  uint64
	Time time.Time
	Tag  Tag
	Text string
}

func (l Line) String() string {
	return fmt.Sprintf("%s [%s] %s", l.Time.Format("2006-01-02 15:04:05"), l.Tag, l.Text)
}

const (
	filePrefix  = "launcher-"
	fileSuffix  = ".log"
	historySize = 500
	maskText    = "***"
	minSecret   = 4
)

// Logger is safe for concurrent use.
type Logger struct {
	dir      string
	keepDays int
	now      func() time.Time

	mu      sync.Mutex
	file    *os.File
	day     string
	secrets map[string]bool
	history []Line
	seq     uint64
	subs    map[chan Line]bool
	closed  bool
}

// New opens the logger writing into dir and deletes log files older than
// keepDays. An empty dir keeps lines in memory only.
func New(dir string, keepDays int) (*Logger, error) {
	return newLogger(dir, keepDays, time.Now)
}

func newLogger(dir string, keepDays int, now func() time.Time) (*Logger, error) {
	l := &Logger{dir: dir, keepDays: keepDays, now: now, secrets: make(map[string]bool), subs: make(map[chan Line]bool)}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		l.prune()
	}
	return l, nil
}

// Printf logs one line; embedded newlines produce several lines.
func (l *Logger) Printf(tag Tag, format string, args ...any) {
	l.write(tag, fmt.Sprintf(format, args...))
}

// AddSecret masks s in every later line. Very short values are ignored to
// avoid mangling ordinary text.
func (l *Logger) AddSecret(s string) {
	if len(s) < minSecret {
		return
	}
	l.mu.Lock()
	l.secrets[s] = true
	l.mu.Unlock()
}

// RemoveSecret stops masking s (for example after a session ends).
func (l *Logger) RemoveSecret(s string) {
	l.mu.Lock()
	delete(l.secrets, s)
	l.mu.Unlock()
}

// Writer adapts the logger for libraries that write whole lines to an
// io.Writer (the core library's log output).
func (l *Logger) Writer(tag Tag) io.Writer { return tagWriter{l: l, tag: tag} }

type tagWriter struct {
	l   *Logger
	tag Tag
}

func (w tagWriter) Write(p []byte) (int, error) {
	w.l.write(w.tag, string(p))
	return len(p), nil
}

// Recent returns up to n of the newest lines, oldest first.
func (l *Logger) Recent(n int) []Line {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > len(l.history) || n <= 0 {
		n = len(l.history)
	}
	return append([]Line(nil), l.history[len(l.history)-n:]...)
}

// Subscribe delivers new lines until cancel is called. Slow subscribers miss
// lines rather than blocking the logger.
func (l *Logger) Subscribe() (<-chan Line, func()) {
	ch := make(chan Line, 256)
	l.mu.Lock()
	l.subs[ch] = true
	l.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			l.mu.Lock()
			if l.subs[ch] {
				delete(l.subs, ch)
				close(ch)
			}
			l.mu.Unlock()
		})
	}
}

// Close flushes and closes the current file and ends subscriptions.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	for ch := range l.subs {
		close(ch)
	}
	l.subs = make(map[chan Line]bool)
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

func (l *Logger) write(tag Tag, text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	for _, part := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		part = strings.TrimRight(part, "\r")
		if part == "" {
			continue
		}
		l.seq++
		line := Line{Seq: l.seq, Time: now, Tag: tag, Text: l.mask(part)}
		l.history = append(l.history, line)
		if len(l.history) > historySize {
			l.history = append([]Line(nil), l.history[len(l.history)-historySize:]...)
		}
		l.appendToFile(line)
		for ch := range l.subs {
			select {
			case ch <- line:
			default:
			}
		}
	}
}

func (l *Logger) mask(text string) string {
	for s := range l.secrets {
		text = strings.ReplaceAll(text, s, maskText)
	}
	return text
}

// appendToFile writes to the file for the line's day, rotating at midnight.
// Errors are swallowed: logging must never break acceleration.
func (l *Logger) appendToFile(line Line) {
	if l.dir == "" {
		return
	}
	day := line.Time.Format("20060102")
	if l.file == nil || day != l.day {
		if l.file != nil {
			l.file.Close()
			l.file = nil
		}
		f, err := os.OpenFile(filepath.Join(l.dir, filePrefix+day+fileSuffix), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		l.file, l.day = f, day
		l.pruneLocked(line.Time)
	}
	fmt.Fprintln(l.file, line.String())
}

func (l *Logger) prune() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.now())
}

// pruneLocked deletes launcher-YYYYMMDD.log files older than keepDays.
func (l *Logger) pruneLocked(now time.Time) {
	if l.keepDays <= 0 {
		return
	}
	names, err := filepath.Glob(filepath.Join(l.dir, filePrefix+"*"+fileSuffix))
	if err != nil {
		return
	}
	sort.Strings(names)
	cutoff := now.AddDate(0, 0, -l.keepDays).Format("20060102")
	for _, name := range names {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(name), filePrefix), fileSuffix)
		if len(day) == 8 && day < cutoff {
			os.Remove(name)
		}
	}
}
