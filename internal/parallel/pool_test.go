package parallel_test

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edsilegxrepo/rsync/internal/parallel"
)

func TestBufferPool(t *testing.T) {
	t.Parallel()
	buf := parallel.GetBuffer()
	if buf == nil || len(*buf) != parallel.DefaultBufferSize {
		t.Fatalf("unexpected buffer from GetBuffer: %v", buf)
	}
	parallel.PutBuffer(buf)
}

func TestDefaultWorkerCount(t *testing.T) {
	t.Parallel()
	want := runtime.NumCPU() * 2
	if got := parallel.DefaultWorkerCount(); got != want {
		t.Errorf("DefaultWorkerCount = %d, want %d", got, want)
	}
}

func TestTuneSocketBuffers(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err == nil {
			parallel.TuneSocketBuffers(conn)
			conn.Close()
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}
	parallel.TuneSocketBuffers(conn)
	conn.Close()
	<-done
}

func TestBatchWorkerPool(t *testing.T) {
	t.Parallel()
	pool := parallel.NewBatchWorkerPool(4)

	var count int64
	numTasks := 100
	tasks := make([]func(ctx context.Context) error, numTasks)

	for i := 0; i < numTasks; i++ {
		tasks[i] = func(ctx context.Context) error {
			atomic.AddInt64(&count, 1)
			time.Sleep(1 * time.Millisecond)
			return nil
		}
	}

	if err := pool.Run(context.Background(), tasks); err != nil {
		t.Fatalf("pool.Run failed: %v", err)
	}

	if got := atomic.LoadInt64(&count); got != int64(numTasks) {
		t.Errorf("processed tasks = %d, want %d", got, numTasks)
	}
}

func TestBatchWorkerPoolErrorCancel(t *testing.T) {
	t.Parallel()
	pool := parallel.NewBatchWorkerPool(4)

	errTest := errors.New("simulated error")
	tasks := []func(ctx context.Context) error{
		func(ctx context.Context) error {
			return errTest
		},
		func(ctx context.Context) error {
			time.Sleep(50 * time.Millisecond)
			return nil
		},
	}

	if err := pool.Run(context.Background(), tasks); !errors.Is(err, errTest) {
		t.Errorf("expected errTest, got %v", err)
	}
}
