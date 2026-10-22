// Package logs parses, groups and streams container logs.
package logs

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

type Level int

const (
	LevelUnknown Level = iota
	LevelTrace
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	}
	return ""
}

func ParseLevel(s string) Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TRACE", "FINEST", "FINER":
		return LevelTrace
	case "DEBUG", "FINE", "DBG":
		return LevelDebug
	case "INFO", "INFORMATION", "NOTICE", "INF":
		return LevelInfo
	case "WARN", "WARNING", "WRN":
		return LevelWarn
	case "ERROR", "ERR", "FATAL", "SEVERE", "CRITICAL", "CRIT", "PANIC", "EMERGENCY", "ALERT":
		return LevelError
	}
	return LevelUnknown
}

type Kind int

const (
	KindLog   Kind = iota
	KindEvent      // Kubernetes event interleaved with logs
	KindMeta       // lazydev's own notices (stream ended, errors)
)

// Line is one raw line from a stream.
type Line struct {
	Source string
	Time   time.Time
	Text   string
	Kind   Kind
	Level  Level // optional preset (events, meta)
}

// Entry is a parsed log record, possibly spanning several raw lines.
type Entry struct {
	Source  string
	Time    time.Time
	Level   Level
	Kind    Kind
	Logger  string
	Message string
	Fields  []string // extra JSON fields as key=value
	Stack   []string // continuation lines (stack traces)
	JSON    bool
}

var (
	jsonLevelKeys   = []string{"level", "severity", "log.level", "@l", "lvl", "loglevel"}
	jsonMessageKeys = []string{"message", "msg", "@m", "@mt", "log"}
	jsonLoggerKeys  = []string{"logger", "logger_name", "log.logger", "loggerName", "name"}
	jsonStackKeys   = []string{"stack_trace", "stacktrace", "exception", "error.stack_trace", "exception.stacktrace", "stack", "@x"}
	jsonTimeKeys    = []string{"@timestamp", "timestamp", "time", "ts", "@t"}
	jsonIgnoredKeys = map[string]bool{
		"ecs.version": true, "process.pid": true, "process.thread.name": true, "thread_name": true,
		"level_value": true, "@version": true, "service.name": true, "service.version": true,
	}

	logfmtLevelRe = regexp.MustCompile(`(?i)\b(?:level|lvl|severity)=["']?([a-z]+)`)
	plainLevelRe  = regexp.MustCompile(`\b(TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|FATAL|SEVERE|CRITICAL)\b`)
	// Spring Boot / Quarkus / Logback default layouts start with "<timestamp> <LEVEL> ".
	plainPrefixRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s+(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\s+`)
	exceptionRe   = regexp.MustCompile(`^[a-zA-Z_$][\w$]*(\.[a-zA-Z_$][\w$]*)+(Exception|Error|Throwable)(:|$)`)
	omittedRe     = regexp.MustCompile(`^\.\.\. \d+ (more|common frames omitted)`)
)

const plainLevelWindow = 80

// Parse turns a raw line into an entry.
func Parse(l Line) Entry {
	e := Entry{Source: l.Source, Time: l.Time, Kind: l.Kind, Level: l.Level, Message: Clean(l.Text)}
	if l.Kind != KindLog {
		return e
	}
	trimmed := strings.TrimSpace(e.Message)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		if parseJSON(trimmed, &e) {
			return e
		}
	}
	if m := plainPrefixRe.FindStringSubmatch(e.Message); m != nil {
		e.Level = ParseLevel(m[1])
		e.Message = e.Message[len(m[0]):]
		return e
	}
	e.Level = plainLevel(e.Message)
	return e
}

func plainLevel(text string) Level {
	if m := logfmtLevelRe.FindStringSubmatch(text); m != nil {
		if lvl := ParseLevel(m[1]); lvl != LevelUnknown {
			return lvl
		}
	}
	window := text
	if len(window) > plainLevelWindow {
		window = window[:plainLevelWindow]
	}
	if m := plainLevelRe.FindString(window); m != "" {
		return ParseLevel(m)
	}
	return LevelUnknown
}

func parseJSON(text string, e *Entry) bool {
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return false
	}
	flat := map[string]any{}
	flatten("", obj, flat)

	used := map[string]bool{}
	take := func(keys []string) string {
		for _, k := range keys {
			if v, ok := flat[k]; ok {
				used[k] = true
				if s := scalar(v); s != "" {
					return s
				}
			}
		}
		return ""
	}
	msg := take(jsonMessageKeys)
	if msg == "" {
		return false
	}
	e.JSON = true
	e.Message = msg
	e.Level = ParseLevel(take(jsonLevelKeys))
	e.Logger = take(jsonLoggerKeys)
	if stack := take(jsonStackKeys); stack != "" {
		for _, line := range strings.Split(strings.TrimRight(stack, "\n"), "\n") {
			e.Stack = append(e.Stack, Clean(line))
		}
	}
	if ts := take(jsonTimeKeys); ts != "" && e.Time.IsZero() {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			e.Time = t
		}
	}
	keys := make([]string, 0, len(flat))
	for k := range flat {
		if !used[k] && !jsonIgnoredKeys[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		e.Fields = append(e.Fields, k+"="+scalar(flat[k]))
	}
	return true
}

func flatten(prefix string, in map[string]any, out map[string]any) {
	for k, v := range in {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if m, ok := v.(map[string]any); ok {
			flatten(key, m, out)
			continue
		}
		out[key] = v
	}
}

func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// Clean removes terminal escape sequences and expands tabs so text has a predictable display width.
func Clean(s string) string {
	return strings.ReplaceAll(ansi.Strip(strings.TrimRight(s, "\r\n")), "\t", "    ")
}

// IsContinuation reports whether a raw line continues the previous entry (stack frames, causes).
func IsContinuation(text string) bool {
	if text == "" {
		return false
	}
	if text[0] == ' ' || text[0] == '\t' {
		return strings.TrimSpace(text) != ""
	}
	return strings.HasPrefix(text, "Caused by: ") ||
		strings.HasPrefix(text, "Suppressed: ") ||
		omittedRe.MatchString(text) ||
		exceptionRe.MatchString(text)
}

// SplitTimestamp separates the RFC3339 prefix added by `--timestamps` from the text.
func SplitTimestamp(raw string) (time.Time, string) {
	if i := strings.IndexByte(raw, ' '); i >= 20 && i <= 40 {
		if t, err := time.Parse(time.RFC3339Nano, raw[:i]); err == nil {
			return t, raw[i+1:]
		}
	}
	return time.Time{}, raw
}
