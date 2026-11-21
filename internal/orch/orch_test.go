package orch

import "testing"

func TestParsePlan(t *testing.T) {
	tasks, err := parsePlan("Here you go:\n[{\"title\":\"A\",\"task\":\"do a\"},{\"title\":\"\",\"task\":\"do b\"},{\"title\":\"C\",\"task\":\" \"}]\nthanks")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].Title != "A" || tasks[1].Title != "do b" {
		t.Fatalf("got %+v", tasks)
	}
	if _, err := parsePlan("no json here"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := parsePlan("[]"); err == nil {
		t.Fatal("expected error for empty list")
	}
}

func TestLastNonEmpty(t *testing.T) {
	cases := map[string]string{
		"done\n\n```\ncode\n```\n": "code",
		"summary line\n---\n":      "summary line",
		"first\nsecond\n\n":        "second",
		"```\n```":                 "",
	}
	for in, want := range cases {
		if got := lastNonEmpty(in); got != want {
			t.Errorf("lastNonEmpty(%q) = %q, want %q", in, got, want)
		}
	}
}
