package progress_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gokrazy/rsync/internal/progress"
)

func TestProgressPrinterUnitsAndRates(t *testing.T) {
	var buf bytes.Buffer
	currTime := time.Now()
	mockNow := func() time.Time {
		return currTime
	}

	p := progress.NewPrinter(&buf, mockNow)
	p.Reset(100 * 1024 * 1024 * 1024) // 100GB

	// 1. Initial show
	p.MaybeShow(0, false)

	// Advance time by 2 seconds and show 5MB
	currTime = currTime.Add(2 * time.Second)
	p.MaybeShow(5*1024*1024, false)

	// Advance time and transfer 50GB (GB/s rate)
	currTime = currTime.Add(2 * time.Second)
	p.Show(50*1024*1024*1024, true)

	out := buf.String()
	if !strings.Contains(out, "GB/s") && !strings.Contains(out, "MB/s") {
		t.Fatalf("Expected rate units in output, got %q", out)
	}
}
