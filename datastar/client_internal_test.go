package datastar

import (
	"bytes"
	"testing"
)

func TestClientPath_ChangesExactlyWhenBytesChange(t *testing.T) {
	a := []byte("// Datastar v1.0.0\nconsole.log(1)\n")
	same := bytes.Clone(a)
	changed := bytes.Clone(a)
	changed[len(changed)-2] = '2'

	if clientPath(a) != clientPath(same) {
		t.Errorf("identical bytes produced different paths: %q vs %q", clientPath(a), clientPath(same))
	}
	if clientPath(a) == clientPath(changed) {
		t.Errorf("a one-byte change kept the path %q", clientPath(a))
	}
	if ClientPath != clientPath(clientJS) {
		t.Errorf("ClientPath = %q, not derived from the embedded client (%q)", ClientPath, clientPath(clientJS))
	}
}
