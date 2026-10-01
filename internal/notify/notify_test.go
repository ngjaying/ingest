package notify

import (
	"strings"
	"testing"
)

func TestBuildToastScript(t *testing.T) {
	s := BuildToastScript(`卡 "EOS" 到了`, "10 个文件")
	if !strings.Contains(s, "ingest") {
		t.Fatal("missing app id")
	}
	if strings.Contains(s, `"EOS"`) {
		t.Fatal("unescaped quotes")
	}
}
