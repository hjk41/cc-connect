package toolallowlist

import (
	"reflect"
	"testing"
)

func TestParseAndAdd(t *testing.T) {
	for _, input := range []any{[]string{"Bash", "bash", " WebFetch ", ""}, []any{"Bash", "bash", " WebFetch ", "", 7, nil}} {
		got := Add(Parse(input), "WEBFETCH", "Patch")
		if want := []string{"Bash", "WebFetch", "Patch"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("Parse/Add(%#v) = %v, want %v", input, got, want)
		}
	}
	input := []string{"Bash"}
	parsed := Parse(input)
	parsed[0] = "Patch"
	if input[0] != "Bash" {
		t.Fatal("Parse aliases input")
	}
}

func TestAllowsExactNamesAndRejectsQuestionsAndUnknownTools(t *testing.T) {
	list := []string{"Bash", "AskUserQuestion", "unknown", "permission", "other", ""}
	for _, tool := range []string{"bash", "BASH"} {
		if !Allows(list, tool) {
			t.Errorf("expected %q to match", tool)
		}
	}
	for _, tool := range []string{"Bash(ls)", "Ba", "WebFetch", "AskUserQuestion", "ASKUSERQUESTION", "unknown", "permission", "other", ""} {
		if Allows(list, tool) {
			t.Errorf("unexpected match for %q", tool)
		}
	}
}
