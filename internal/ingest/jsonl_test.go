package ingest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readSample(t *testing.T) []FAQ {
	t.Helper()
	f, err := os.Open("testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	fs, err := ReadJSONL(f)
	if err != nil {
		t.Fatalf("ReadJSONL: %v", err)
	}
	return fs
}

func TestReadJSONLAssignsLineNumbers(t *testing.T) {
	fs := readSample(t)
	if len(fs) != 3 {
		t.Fatalf("len = %d, want 3", len(fs))
	}
	for i, f := range fs {
		if f.ID != i {
			t.Errorf("fs[%d].ID = %d, want %d", i, f.ID, i)
		}
	}
	want := FAQ{ID: 1, Question: "社員証をなくしたときは？", Answer: "総務課に届け出てください。",
		Agency: "架空の省", URL: "https://example.com/faq/2", QANorm: "社員証をなくしたときは?\n総務課に届け出てください。"}
	if fs[1] != want {
		t.Errorf("fs[1] = %+v, want %+v", fs[1], want)
	}
}

func TestReadJSONLSetsQANorm(t *testing.T) {
	fs := readSample(t)
	if want := "PCのパスワードを忘れたときは?\n情報システム課の窓口で再設定を申し込んでください。"; fs[0].QANorm != want {
		t.Errorf("QANorm = %q, want %q", fs[0].QANorm, want)
	}
	if fs[0].Question != "ＰＣのパスワードを忘れたときは？" {
		t.Errorf("Question must keep the original text, got %q", fs[0].Question)
	}
}

func TestReadJSONLRejectsMissingField(t *testing.T) {
	for _, field := range []string{"Question", "Answer", "copyright", "url"} {
		row := map[string]string{"Question": "q", "Answer": "a", "copyright": "c", "url": "u"}
		delete(row, field)
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		in := `{"Question":"q","Answer":"a","copyright":"c","url":"u"}` + "\n" + string(line) + "\n"
		_, err = ReadJSONL(strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), field) {
			t.Errorf("missing %s: err = %v, want an error naming line 2 and the field", field, err)
		}
	}
}

func TestReadJSONLRejectsBrokenJSON(t *testing.T) {
	_, err := ReadJSONL(strings.NewReader(`{"Question":"q",` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("err = %v, want an error naming line 1", err)
	}
}

func TestReadJSONLAcceptsLongLine(t *testing.T) {
	long := strings.Repeat("あ", 300_000) // 900KB in UTF-8, beyond bufio's 64KiB default
	in := `{"Question":"q","Answer":"` + long + `","copyright":"c","url":"u"}` + "\n"
	fs, err := ReadJSONL(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ReadJSONL: %v", err)
	}
	if len(fs) != 1 || fs[0].Answer != long {
		t.Fatalf("long answer was not read intact")
	}
}

func TestReadJSONLRejectsLineOverLimit(t *testing.T) {
	in := `{"Question":"q","Answer":"` + strings.Repeat("a", maxLine) + `","copyright":"c","url":"u"}` + "\n"
	if _, err := ReadJSONL(strings.NewReader(in)); err == nil {
		t.Fatal("want an error for a line longer than maxLine")
	}
}

func TestReadJSONLEmptyInput(t *testing.T) {
	fs, err := ReadJSONL(strings.NewReader(""))
	if err != nil || len(fs) != 0 {
		t.Fatalf("got %v, %v; want no rows and no error", fs, err)
	}
}
