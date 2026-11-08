//go:build !windows

package execx

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// An installer that exits 0 but leaves a child holding stdout succeeds.
func TestRunForgivesChildHoldingPipes(t *testing.T) {
	var out bytes.Buffer
	start := time.Now()
	err := Run(context.Background(), time.Minute, &out, nil, "/bin/sh", "-c", "echo installed; (sleep 5 >&1 &) ; exit 0")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("waited for the child")
	}
	if err := Run(context.Background(), time.Minute, &out, nil, "/bin/sh", "-c", "(sleep 5 >&1 &) ; exit 3"); err == nil {
		t.Fatal("failure exit forgiven")
	}
}
