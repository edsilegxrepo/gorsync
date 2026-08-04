package rsyncostest

import (
	"testing"

	"github.com/edsilegxrepo/gorsync/internal/rsyncos"
	"github.com/edsilegxrepo/gorsync/internal/testlogger"
)

func New(t *testing.T) *rsyncos.Env {
	return &rsyncos.Env{
		// Logs go to stderr, so wire that up to a testlogger.
		Stderr: testlogger.New(t),
	}
}
