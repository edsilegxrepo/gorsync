package rsyncwire

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// ReadVarInt reads an rsync protocol 30+ variable-length integer.
func ReadVarInt(r io.Reader) (int64, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	first := b[0]
	if first < 0x80 {
		return int64(first), nil
	}

	var cnt int
	var mask byte
	if (first & 0xc0) == 0x80 {
		cnt = 1
		mask = 0x3f
	} else if (first & 0xe0) == 0xc0 {
		cnt = 2
		mask = 0x1f
	} else if (first & 0xf0) == 0xe0 {
		cnt = 3
		mask = 0x0f
	} else if (first & 0xf8) == 0xf0 {
		cnt = 4
		mask = 0x07
	} else if (first & 0xfc) == 0xf8 {
		cnt = 5
		mask = 0x03
	} else if (first & 0xfe) == 0xfc {
		cnt = 6
		mask = 0x01
	} else if first == 0xfe {
		cnt = 7
		mask = 0x00
	} else {
		cnt = 8
		mask = 0x00
	}

	val := int64(first & mask)
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:cnt]); err != nil {
		return 0, err
	}

	for i := 0; i < cnt; i++ {
		val = (val << 8) | int64(buf[i])
	}
	return val, nil
}

// WriteVarInt writes an rsync protocol 30+ variable-length integer.
func WriteVarInt(w io.Writer, val int64) error {
	if val < 0 {
		return fmt.Errorf("negative varint not supported: %d", val)
	}
	if val < (1 << 7) {
		var buf [1]byte
		buf[0] = byte(val)
		_, err := w.Write(buf[:])
		return err
	}
	if val < (1 << 14) {
		var buf [2]byte
		buf[0] = byte(0x80 | (val >> 8))
		buf[1] = byte(val & 0xff)
		_, err := w.Write(buf[:])
		return err
	}
	if val < (1 << 21) {
		var buf [3]byte
		buf[0] = byte(0xc0 | (val >> 16))
		buf[1] = byte((val >> 8) & 0xff)
		buf[2] = byte(val & 0xff)
		_, err := w.Write(buf[:])
		return err
	}
	if val < (1 << 28) {
		var buf [4]byte
		buf[0] = byte(0xe0 | (val >> 24))
		buf[1] = byte((val >> 16) & 0xff)
		buf[2] = byte((val >> 8) & 0xff)
		buf[3] = byte(val & 0xff)
		_, err := w.Write(buf[:])
		return err
	}
	if val < (int64(1) << 35) {
		var buf [5]byte
		buf[0] = byte(0xf0 | (val >> 32))
		buf[1] = byte((val >> 24) & 0xff)
		buf[2] = byte((val >> 16) & 0xff)
		buf[3] = byte((val >> 8) & 0xff)
		buf[4] = byte(val & 0xff)
		_, err := w.Write(buf[:])
		return err
	}
	if val < (int64(1) << 42) {
		var buf [6]byte
		buf[0] = byte(0xf8 | (val >> 40))
		buf[1] = byte((val >> 32) & 0xff)
		buf[2] = byte((val >> 24) & 0xff)
		buf[3] = byte((val >> 16) & 0xff)
		buf[4] = byte((val >> 8) & 0xff)
		buf[5] = byte(val & 0xff)
		_, err := w.Write(buf[:])
		return err
	}
	if val < (int64(1) << 49) {
		var buf [7]byte
		buf[0] = byte(0xfc | (val >> 48))
		buf[1] = byte((val >> 40) & 0xff)
		buf[2] = byte((val >> 32) & 0xff)
		buf[3] = byte((val >> 24) & 0xff)
		buf[4] = byte((val >> 16) & 0xff)
		buf[5] = byte((val >> 8) & 0xff)
		buf[6] = byte(val & 0xff)
		_, err := w.Write(buf[:])
		return err
	}
	if val < (int64(1) << 56) {
		var buf [8]byte
		buf[0] = byte(0xfe | (val >> 56))
		buf[1] = byte((val >> 48) & 0xff)
		buf[2] = byte((val >> 40) & 0xff)
		buf[3] = byte((val >> 32) & 0xff)
		buf[4] = byte((val >> 24) & 0xff)
		buf[5] = byte((val >> 16) & 0xff)
		buf[6] = byte((val >> 8) & 0xff)
		buf[7] = byte(val & 0xff)
		_, err := w.Write(buf[:])
		return err
	}

	var buf [9]byte
	buf[0] = 0xff
	binary.BigEndian.PutUint64(buf[1:], uint64(val))
	_, err := w.Write(buf[:])
	return err
}

// ReadVarInt32 reads a 32-bit protocol 32 varint integer.
func ReadVarInt32(r io.Reader) (int32, error) {
	val, err := ReadVarInt(r)
	return int32(val), err
}

// WriteVarInt32 writes a 32-bit protocol 32 varint integer.
func WriteVarInt32(w io.Writer, val int32) error {
	return WriteVarInt(w, int64(val))
}

// ReadTime64 reads a Protocol 32 64-bit nanosecond precision timestamp.
func ReadTime64(r io.Reader) (time.Time, error) {
	sec, err := ReadVarInt(r)
	if err != nil {
		return time.Time{}, err
	}
	nsec, err := ReadVarInt(r)
	if err != nil {
		return time.Unix(sec, 0), nil
	}
	return time.Unix(sec, nsec), nil
}

// WriteTime64 writes a Protocol 32 64-bit nanosecond precision timestamp.
func WriteTime64(w io.Writer, t time.Time) error {
	if err := WriteVarInt(w, t.Unix()); err != nil {
		return err
	}
	return WriteVarInt(w, int64(t.Nanosecond()))
}
