package proc

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// A cancelled program returns at once, even when a child of its own keeps
// the output open.
func TestCancelStopsTheProgramAndItsChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = Command(ctx, "sh", "-c", "sleep 30 & sleep 30").Output()
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s", took)
	}
}
