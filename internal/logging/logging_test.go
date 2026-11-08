package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.log")
	r, err := NewRotator(p, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	line := bytes.Repeat([]byte("x"), 39)
	line = append(line, '\n')
	for i := 0; i < 20; i++ {
		if _, err := r.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	r.Close()
	for _, name := range []string{"agent.log", "agent.log.1", "agent.log.2", "agent.log.3"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
		if st.Size() > 100 {
			t.Fatalf("%s is %d bytes", name, st.Size())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "agent.log.4")); err == nil {
		t.Fatal("kept more than 3 old files")
	}
}
