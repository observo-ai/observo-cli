package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestResult_JSONModeEmitsIndentedJSON(t *testing.T) {
	var buf bytes.Buffer
	p := &Printer{JSON: true, Out: &buf}
	err := p.Result(map[string]any{"k": "v"}, "human-line-ignored")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"k": "v"`) {
		t.Errorf("expected JSON, got:\n%s", out)
	}
	if strings.Contains(out, "human-line-ignored") {
		t.Errorf("human line leaked into JSON output:\n%s", out)
	}
}

func TestResult_TextModeEmitsHumanLine(t *testing.T) {
	var buf bytes.Buffer
	p := &Printer{JSON: false, Out: &buf}
	if err := p.Result(map[string]any{"k": "v"}, "the human line"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "the human line\n" {
		t.Errorf("text mode: got %q", buf.String())
	}
}

func TestResult_TextModeEmptyHumanLineWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	p := &Printer{JSON: false, Out: &buf}
	if err := p.Result(struct{}{}, ""); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty human line should write nothing; got %q", buf.String())
	}
}
