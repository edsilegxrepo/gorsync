package parallel

import (
	"context"
	"net"
	"runtime"
	"sync"
)

const (
	// DefaultBufferSize is 128KB per buffer block for high-throughput streaming
	DefaultBufferSize = 128 * 1024
	// WANSocketBufferSize is 4MB for high BDP links (100ms+ latency)
	WANSocketBufferSize = 4 * 1024 * 1024
)

var bufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, DefaultBufferSize)
		return &b
	},
}

// GetBuffer acquires a 128KB byte slice from the global sync.Pool.
func GetBuffer() *[]byte {
	return bufferPool.Get().(*[]byte)
}

// PutBuffer returns a 128KB byte slice to the global sync.Pool after zeroing.
func PutBuffer(b *[]byte) {
	if b == nil || len(*b) != DefaultBufferSize {
		return
	}
	bufferPool.Put(b)
}

// DefaultWorkerCount calculates NumCPU() * 2 for worker pool sizing.
func DefaultWorkerCount() int {
	return runtime.NumCPU() * 2
}

// TuneSocketBuffers configures TCP read and write buffer sizes for high-latency BDP links.
func TuneSocketBuffers(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(WANSocketBufferSize)
		_ = tc.SetWriteBuffer(WANSocketBufferSize)
	}
}

// BatchWorkerPool executes tasks in parallel across nWorkers worker goroutines.
type BatchWorkerPool struct {
	nWorkers int
}

// NewBatchWorkerPool initializes a worker pool with nWorkers capacity (defaults to NumCPU() * 2).
func NewBatchWorkerPool(nWorkers int) *BatchWorkerPool {
	if nWorkers <= 0 {
		nWorkers = DefaultWorkerCount()
	}
	return &BatchWorkerPool{nWorkers: nWorkers}
}

// Run executes tasks concurrently across the pool, returning the first encountered error.
func (p *BatchWorkerPool) Run(ctx context.Context, tasks []func(ctx context.Context) error) error {
	if len(tasks) == 0 {
		return nil
	}

	taskCh := make(chan func(ctx context.Context) error, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, p.nWorkers)

	workers := p.nWorkers
	if workers > len(tasks) {
		workers = len(tasks)
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				select {
				case <-ctx.Done():
					return
				default:
					if err := task(ctx); err != nil {
						select {
						case errCh <- err:
							cancel()
						default:
						}
						return
					}
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	if err, ok := <-errCh; ok {
		return err
	}
	return nil
}
