package hooks

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseKnownFields(t *testing.T) {
	in := `{"session_id":"abc","transcript_path":"/t.jsonl","cwd":"/w","hook_event_name":"PreToolUse",
	"tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/x","nested":[1,2,{"a":"b"}]},"tool_use_id":"toolu_1",
	"agent_id":"ag","future_field":{"x":1}}`
	ev, err := Parse([]byte(in), false)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != KindPreTool || ev.SessionID != "abc" || ev.ToolName != "Bash" || ev.ToolUseID != "toolu_1" || ev.AgentID != "ag" {
		t.Fatalf("parsed %+v", ev)
	}
}

func TestParseErrorFieldMeaning(t *testing.T) {
	sf, _ := Parse([]byte(`{"session_id":"s","hook_event_name":"StopFailure","error":"rate_limit","error_details":"429 Too Many Requests"}`), false)
	if sf.ErrorType != "rate_limit" || sf.ErrorDetail != "429 Too Many Requests" {
		t.Fatalf("StopFailure: %+v", sf)
	}
	// The error key comes before the event name here.
	tf, _ := Parse([]byte(`{"error":"Exit code 1\nboom","session_id":"s","hook_event_name":"PostToolUseFailure","is_interrupt":true}`), false)
	if tf.ErrorType != "" || tf.ErrorDetail != "Exit code 1 boom" || !tf.IsInterrupt {
		t.Fatalf("PostToolUseFailure: %+v", tf)
	}
}

func TestParseUnknownEvent(t *testing.T) {
	ev, err := Parse([]byte(`{"session_id":"s","hook_event_name":"BrandNewEvent"}`), false)
	if err != nil || ev.Kind != KindUnknown {
		t.Fatalf("kind %q err %v", ev.Kind, err)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	for _, in := range []string{``, `[]`, `"x"`, `{"hook_event_name":"Stop"}`, `{"session_id":`} {
		if _, err := Parse([]byte(in), false); err == nil {
			t.Errorf("Parse(%q) returned no error", in)
		}
	}
}

func TestParseTruncatedKeepsLeadingFields(t *testing.T) {
	full := `{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Write","tool_use_id":"t","tool_input":{"content":"` + strings.Repeat("x", 1000) + `"}}`
	data, truncated, err := ReadLimited(strings.NewReader(full), 200)
	if err != nil || !truncated || len(data) != 200 {
		t.Fatalf("ReadLimited: %d %v %v", len(data), truncated, err)
	}
	ev, err := Parse(data, truncated)
	if err != nil || ev.Kind != KindPostTool || ev.ToolUseID != "t" || !ev.Truncated {
		t.Fatalf("parsed %+v, err %v", ev, err)
	}
}

func TestParseSanitizesAndBounds(t *testing.T) {
	long := strings.Repeat("é", 5000)
	in := `{"session_id":"s\u001b[31m","hook_event_name":"UserPromptSubmit","prompt":"hi \u001b]0;pwned\u0007there ` + long + `"}`
	ev, err := Parse([]byte(in), false)
	if err != nil {
		t.Fatal(err)
	}
	if ev.SessionID != "s" {
		t.Fatalf("session %q", ev.SessionID)
	}
	if strings.ContainsRune(ev.Prompt, 0x1b) || strings.Contains(ev.Prompt, "pwned") {
		t.Fatalf("escape kept: %q", ev.Prompt[:30])
	}
	if n := len([]rune(ev.Prompt)); n > MaxPromptExcerpt {
		t.Fatalf("prompt has %d runes", n)
	}
}

func TestEventJSONOmitsExcerpts(t *testing.T) {
	ev, _ := Parse([]byte(`{"session_id":"s","hook_event_name":"Stop","last_assistant_message":"secret text"}`), false)
	var b bytes.Buffer
	b.WriteString(ev.AssistantMessage)
	if b.String() != "secret text" {
		t.Fatal("assistant message not parsed")
	}
}
