package logs

import (
	"testing"
	"time"
)

func TestParsePlainSpringLine(t *testing.T) {
	e := Parse(Line{Text: "2026-10-20T06:43:46.470Z  WARN 1 --- [nio-8080-exec-9] o.s.b.mail.health.MailHealthIndicator    : Mail health check failed"})
	if e.Level != LevelWarn || e.JSON || e.Message != "1 --- [nio-8080-exec-9] o.s.b.mail.health.MailHealthIndicator    : Mail health check failed" {
		t.Errorf("entry = %+v", e)
	}
	if e := Parse(Line{Text: "2026-10-21 11:40:15,215 INFO  [io.quarkus] (main) Keycloak started"}); e.Level != LevelInfo || e.Message != "[io.quarkus] (main) Keycloak started" {
		t.Errorf("quarkus entry = %+v", e)
	}
	if e := Parse(Line{Text: `time=2026 level=error msg="boom"`}); e.Level != LevelError {
		t.Errorf("logfmt level = %v", e.Level)
	}
	if e := Parse(Line{Text: "GET /health 200 - user said INFO is fine but this is past the level window............................."}); e.Level != LevelInfo {
		t.Errorf("keyword inside window should count, got %v", e.Level)
	}
	long := "plain text with no level at all, padded out well beyond the eighty character window so that ERROR here is ignored"
	if e := Parse(Line{Text: long}); e.Level != LevelUnknown {
		t.Errorf("keyword outside window should be ignored, got %v", e.Level)
	}
}

func TestParseJSONLine(t *testing.T) {
	e := Parse(Line{Source: "pod", Text: `{"@timestamp":"2026-10-21T10:00:00.5Z","log.level":"ERROR","message":"payment failed","log.logger":"com.sr.Payment",` +
		`"process.pid":1,"orderId":42,"error":{"stack_trace":"java.lang.IllegalStateException: x\n\tat a.b(C.java:1)"}}`})
	if !e.JSON || e.Level != LevelError || e.Message != "payment failed" || e.Logger != "com.sr.Payment" {
		t.Fatalf("entry = %+v", e)
	}
	if len(e.Stack) != 2 || e.Stack[1] != "    at a.b(C.java:1)" {
		t.Errorf("stack = %q", e.Stack)
	}
	if len(e.Fields) != 1 || e.Fields[0] != "orderId=42" {
		t.Errorf("fields = %v", e.Fields)
	}
	if !e.Time.Equal(time.Date(2026, 10, 21, 10, 0, 0, 5e8, time.UTC)) {
		t.Errorf("time = %v", e.Time)
	}

	nested := Parse(Line{Text: `{"log":{"level":"warn"},"msg":"slow"}`})
	if nested.Level != LevelWarn || nested.Message != "slow" {
		t.Errorf("nested = %+v", nested)
	}
	if notLog := Parse(Line{Text: `{"foo":1}`}); notLog.JSON {
		t.Error("JSON without a message key should stay plain")
	}
}

func TestIsContinuation(t *testing.T) {
	yes := []string{
		"\tat org.example.Foo.bar(Foo.java:10)",
		"    at x",
		"Caused by: java.io.IOException: closed",
		"... 158 common frames omitted",
		"java.lang.IllegalStateException: boom",
		"org.eclipse.angus.mail.MailConnectException",
	}
	no := []string{"", "   ", "2026-10-20 INFO started", "Exception in thread main", "plain"}
	for _, s := range yes {
		if !IsContinuation(s) {
			t.Errorf("%q should be a continuation", s)
		}
	}
	for _, s := range no {
		if IsContinuation(s) {
			t.Errorf("%q should not be a continuation", s)
		}
	}
}

func TestBufferGroupsStacksPerSource(t *testing.T) {
	b := NewBuffer(100)
	t0 := time.Unix(1000, 0)
	b.Add(Line{Source: "a", Time: t0, Text: "ERROR failed"})
	b.Add(Line{Source: "b", Time: t0.Add(time.Second), Text: "INFO other"})
	b.Add(Line{Source: "a", Time: t0.Add(2 * time.Second), Text: "java.lang.RuntimeException: x"})
	b.Add(Line{Source: "a", Time: t0.Add(2 * time.Second), Text: "\tat Foo.bar(Foo.java:1)"})

	if b.Len() != 2 {
		t.Fatalf("len = %d", b.Len())
	}
	if got := b.At(0).Stack; len(got) != 2 {
		t.Errorf("stack of a = %q", got)
	}
	if b.At(1).Source != "b" {
		t.Errorf("order wrong: %+v", b.At(1))
	}
}

func TestBufferKeepsStackAfterBlankLine(t *testing.T) {
	b := NewBuffer(100)
	b.Add(Line{Source: "a", Text: "2026-10-20T06:43:46.470Z  WARN 1 --- [exec-9] MailHealthIndicator : Mail health check failed"})
	b.Add(Line{Source: "a", Text: ""})
	b.Add(Line{Source: "a", Text: "org.eclipse.angus.mail.util.MailConnectException: Couldn't connect"})
	b.Add(Line{Source: "a", Text: "\tat jakarta.mail.Service.connect(Service.java:345)"})

	if b.Len() != 1 {
		t.Fatalf("len = %d, want the stack attached to the WARN entry", b.Len())
	}
	if got := b.At(0).Stack; len(got) != 2 {
		t.Errorf("stack = %q", got)
	}
}

func TestBufferOrdersByTime(t *testing.T) {
	b := NewBuffer(100)
	t0 := time.Unix(1000, 0)
	b.Add(Line{Source: "a", Time: t0.Add(3 * time.Second), Text: "third"})
	b.Add(Line{Source: "b", Time: t0.Add(1 * time.Second), Text: "first"})
	b.Add(Line{Source: "b", Time: t0.Add(2 * time.Second), Text: "second"})
	b.Add(Line{Source: "meta", Kind: KindMeta, Text: "untimed"})
	var got []string
	for _, e := range b.Entries() {
		got = append(got, e.Message)
	}
	want := []string{"first", "second", "third", "untimed"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestBufferTrimsToCapacity(t *testing.T) {
	b := NewBuffer(10)
	for i := range 25 {
		b.Add(Line{Source: "a", Time: time.Unix(int64(i), 0), Text: "line"})
	}
	if b.Len() > 10 || b.Dropped() == 0 {
		t.Errorf("len = %d dropped = %d", b.Len(), b.Dropped())
	}
	if last := b.At(b.Len() - 1); last.Time.Unix() != 24 {
		t.Errorf("newest entry lost: %v", last.Time)
	}
}

func TestHintForInotifyExhaustion(t *testing.T) {
	if Hint("failed to create fsnotify watcher: too many open files") == "" {
		t.Error("expected a hint for kubelet inotify exhaustion")
	}
	if Hint("too many open files") != "" {
		t.Error("unrelated errors should not get the inotify hint")
	}
}

func TestSplitTimestamp(t *testing.T) {
	ts, text := SplitTimestamp("2026-10-21T10:53:14.870278813Z WARNING: deprecated")
	if ts.IsZero() || text != "WARNING: deprecated" {
		t.Errorf("ts=%v text=%q", ts, text)
	}
	if ts, text := SplitTimestamp("no timestamp here"); !ts.IsZero() || text != "no timestamp here" {
		t.Errorf("ts=%v text=%q", ts, text)
	}
}
