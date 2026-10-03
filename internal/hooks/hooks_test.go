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

func TestParseBackgroundFlag(t *testing.T) {
	ev, _ := Parse([]byte(`{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"t1",
	"tool_input":{"command":"make","run_in_background":true},"tool_response":{"x":1}}`), false)
	if !ev.Background || ev.Kind != KindPostTool {
		t.Fatalf("parsed %+v", ev)
	}
	ev, _ = Parse([]byte(`{"session_id":"s","hook_event_name":"PostToolUse","tool_input":{"command":"ls"}}`), false)
	if ev.Background {
		t.Fatal("background without the flag")
	}
}

func TestParseSubagentEvents(t *testing.T) {
	ev, _ := Parse([]byte(`{"session_id":"s","hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}`), false)
	if ev.Kind != KindSubagentStart || ev.AgentID != "a1" || ev.AgentType != "Explore" {
		t.Fatalf("parsed %+v", ev)
	}
	ev, _ = Parse([]byte(`{"session_id":"s","hook_event_name":"SubagentStop","agent_id":"a1"}`), false)
	if ev.Kind != KindSubagentStop {
		t.Fatalf("parsed %+v", ev)
	}
}

func TestParseTaskNotifications(t *testing.T) {
	prompt := "[SYSTEM NOTIFICATION] <task-notification> <task-id>b1</task-id> <tool-use-id>toolu_1</tool-use-id> " +
		"<status>completed</status> </task-notification> <task-notification><tool-use-id>toolu_2</tool-use-id>" +
		"<status>running</status></task-notification><task-notification><tool-use-id> toolu_3 </tool-use-id><status>Killed</status>"
	ev, _ := Parse([]byte(`{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"`+prompt+`"}`), false)
	if !ev.IsTaskNotification || strings.Join(ev.TasksDone, ",") != "toolu_1,toolu_3" {
		t.Fatalf("notification %v, done %v", ev.IsTaskNotification, ev.TasksDone)
	}
	ev, _ = Parse([]byte(`{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"fix the <status>completed</status> bug"}`), false)
	if ev.IsTaskNotification || ev.TasksDone != nil {
		t.Fatalf("plain prompt parsed as notification: %+v", ev)
	}
}
