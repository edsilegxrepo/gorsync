package rsyncwire

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	t.Parallel()

	// 100 KiB/sec limit = ~102.4 KiB/sec
	limiter := NewRateLimiter(100)
	if limiter == nil {
		t.Fatal("expected non-nil limiter")
	}

	data := make([]byte, 50*1024)
	r := &RateLimitedReader{
		R:       bytes.NewReader(data),
		Limiter: limiter,
		Ctx:     context.Background(),
	}

	start := time.Now()
	buf := make([]byte, 10*1024)
	for {
		_, err := r.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	t.Logf("Read 50KiB in %v with 100KiB/s limit", elapsed)
}
