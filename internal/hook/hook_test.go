package hook

import (
	"testing"
)

func TestExpand(t *testing.T) {
	got := Expand(`rate {target} --device {device}`, map[string]string{
		"target": `D:\stage`, "device": "canon-r7",
	})
	if got != `rate D:\stage --device canon-r7` {
		t.Fatalf("got %q", got)
	}
	if got := Expand("echo {unknown}", nil); got != "echo {unknown}" {
		t.Fatalf("unknown var must stay: %q", got)
	}
}

func TestRunFailureRecorded(t *testing.T) {
	r := Run("exit 3")
	if r.Code == 0 || r.Err == "" {
		t.Fatalf("failure must be recorded: %+v", r)
	}
}
