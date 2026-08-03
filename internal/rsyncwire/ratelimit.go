package rsyncwire

import (
	"context"
	"io"
	"sync"
	"time"
)

// RateLimiter implements a token bucket algorithm to limit transfer bandwidth in KiB/s (1024 bytes/sec).
type RateLimiter struct {
	mu            sync.Mutex
	bytesPerSec   int64
	tokens        float64
	maxTokens     float64
	lastReplenish time.Time
}

func NewRateLimiter(kibPerSec int) *RateLimiter {
	if kibPerSec <= 0 {
		return nil
	}
	bytesPerSec := int64(kibPerSec) * 1024
	return &RateLimiter{
		bytesPerSec:   bytesPerSec,
		tokens:        float64(bytesPerSec),
		maxTokens:     float64(bytesPerSec),
		lastReplenish: time.Now(),
	}
}

func (r *RateLimiter) Wait(ctx context.Context, n int) error {
	if r == nil || r.bytesPerSec <= 0 || n <= 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if target := float64(n); target > r.maxTokens {
		r.maxTokens = target
	}

	for {
		now := time.Now()
		elapsed := now.Sub(r.lastReplenish).Seconds()
		r.lastReplenish = now

		r.tokens += elapsed * float64(r.bytesPerSec)
		if r.tokens > r.maxTokens {
			r.tokens = r.maxTokens
		}

		if r.tokens >= float64(n) {
			r.tokens -= float64(n)
			if r.maxTokens > float64(r.bytesPerSec) && r.tokens < float64(r.bytesPerSec) {
				r.maxTokens = float64(r.bytesPerSec)
			}
			return nil
		}

		needed := float64(n) - r.tokens
		waitDuration := time.Duration((needed / float64(r.bytesPerSec)) * float64(time.Second))

		r.mu.Unlock()
		select {
		case <-ctx.Done():
			r.mu.Lock()
			return ctx.Err()
		case <-time.After(waitDuration):
		}
		r.mu.Lock()
	}
}

type RateLimitedReader struct {
	R       io.Reader
	Limiter *RateLimiter
	Ctx     context.Context
}

func (r *RateLimitedReader) Read(p []byte) (int, error) {
	n, err := r.R.Read(p)
	if n > 0 && r.Limiter != nil {
		ctx := r.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		_ = r.Limiter.Wait(ctx, n)
	}
	return n, err
}

type RateLimitedWriter struct {
	W       io.Writer
	Limiter *RateLimiter
	Ctx     context.Context
}

func (w *RateLimitedWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && w.Limiter != nil {
		ctx := w.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := w.Limiter.Wait(ctx, len(p)); err != nil {
			return 0, err
		}
	}
	return w.W.Write(p)
}
