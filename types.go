package rsync

import (
	"fmt"

	"github.com/edsilegxrepo/gorsync/internal/rsyncwire"
)

// SumBuf represents a single block checksum tuple (Adler32 fast sum1 + MD4/MD5/SHA256 strong sum2)
// used during the rsync delta matching algorithm.
// rsync/rsync.h:struct sum_buf
type SumBuf struct {
	Offset int64
	Len    int64
	Index  int32
	Sum1   uint32
	Sum2   [16]byte
}

// SumHead represents the header and collection of block checksums exchanged between
// sender and receiver during delta synchronization.
// TODO: remove connection.go:sumHead in favor of this type
type SumHead struct {
	// “number of blocks” (openrsync)
	// “how many chunks” (rsync)
	ChecksumCount int32

	// “block length in the file” (openrsync)
	// maximum (1 << 29) for older rsync, (1 << 17) for newer
	BlockLength int32

	// “long checksum length” (openrsync)
	ChecksumLength int32

	// “terminal (remainder) block length” (openrsync)
	// RemainderLength is flength % BlockLength
	RemainderLength int32

	Sums []SumBuf
}

func (sh *SumHead) ReadFrom(c *rsyncwire.Conn) error {
	// TODO(protocol>=30): update maxBlockLen
	const maxBlockLen = 1 << 29 // see rsync.h:OLD_MAX_BLOCK_SIZE

	var err error
	sh.ChecksumCount, err = c.ReadInt32()
	if err != nil {
		return err
	}
	if sh.ChecksumCount < 0 {
		return fmt.Errorf("invalid checksum count %d", sh.ChecksumCount)
	}

	sh.BlockLength, err = c.ReadInt32()
	if err != nil {
		return err
	}
	if sh.BlockLength < 0 || sh.BlockLength > maxBlockLen {
		return fmt.Errorf("invalid block length %d", sh.BlockLength)
	}

	sh.ChecksumLength, err = c.ReadInt32()
	if err != nil {
		return err
	}
	// Support up to 32-byte checksums (MD4/MD5=16, SHA1=20, SHA256/xxHash=32)
	if sh.ChecksumLength < 0 || sh.ChecksumLength > 32 {
		return fmt.Errorf("invalid checksum length %d", sh.ChecksumLength)
	}

	sh.RemainderLength, err = c.ReadInt32()
	if err != nil {
		return err
	}
	if sh.RemainderLength < 0 || sh.RemainderLength > sh.BlockLength {
		return fmt.Errorf("invalid remainder length %d", sh.RemainderLength)
	}

	return nil
}

func (sh *SumHead) WriteTo(c *rsyncwire.Conn) error {
	var buf rsyncwire.Buffer
	buf.WriteInt32(sh.ChecksumCount)
	buf.WriteInt32(sh.BlockLength)
	buf.WriteInt32(sh.ChecksumLength)
	buf.WriteInt32(sh.RemainderLength)
	return c.WriteString(buf.String())
}
