package fastcgi

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// FastCGI record header size.
const headerSize = 8

// FastCGI protocol version.
const version1 = 1

// Maximum record content length.
const maxContentLength = 65535

// Record types.
const (
	TypeBeginRequest    uint8 = 1
	TypeAbortRequest    uint8 = 2
	TypeEndRequest      uint8 = 3
	TypeParams          uint8 = 4
	TypeStdin           uint8 = 5
	TypeStdout          uint8 = 6
	TypeStderr          uint8 = 7
	TypeGetValues       uint8 = 8
	TypeGetValuesResult uint8 = 9
)

// Roles.
const (
	RoleResponder  uint16 = 1
	RoleAuthorizer uint16 = 2
	RoleFilter     uint16 = 3
)

// Flags for BeginRequest.
const (
	FlagKeepConn uint8 = 1
)

// Header is an 8-byte FastCGI record header.
type Header struct {
	Version       uint8
	Type          uint8
	RequestID     uint16
	ContentLength uint16
	PaddingLength uint8
	Reserved      uint8
}

// Record is a complete FastCGI record (header + content).
type Record struct {
	Header
	Content []byte
}

// EncodeHeader writes an 8-byte header to the given writer.
func EncodeHeader(w io.Writer, h *Header) error {
	var buf [headerSize]byte
	buf[0] = h.Version
	buf[1] = h.Type
	binary.BigEndian.PutUint16(buf[2:4], h.RequestID)
	binary.BigEndian.PutUint16(buf[4:6], h.ContentLength)
	buf[6] = h.PaddingLength
	buf[7] = h.Reserved
	_, err := w.Write(buf[:])
	return err
}

// DecodeHeader reads an 8-byte header from the given reader.
func DecodeHeader(r io.Reader) (*Header, error) {
	var buf [headerSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return nil, err
	}
	return &Header{
		Version:       buf[0],
		Type:          buf[1],
		RequestID:     binary.BigEndian.Uint16(buf[2:4]),
		ContentLength: binary.BigEndian.Uint16(buf[4:6]),
		PaddingLength: buf[6],
		Reserved:      buf[7],
	}, nil
}

// WriteRecord writes a complete record (header + content + padding).
func WriteRecord(w io.Writer, recType uint8, requestID uint16, content []byte) error {
	contentLen := len(content)
	padding := (8 - contentLen%8) % 8

	h := &Header{
		Version:       version1,
		Type:          recType,
		RequestID:     requestID,
		ContentLength: uint16(contentLen),
		PaddingLength: uint8(padding),
	}

	if err := EncodeHeader(w, h); err != nil {
		return err
	}
	if contentLen > 0 {
		if _, err := w.Write(content); err != nil {
			return err
		}
	}
	if padding > 0 {
		pad := make([]byte, padding)
		if _, err := w.Write(pad); err != nil {
			return err
		}
	}
	return nil
}

// ReadRecord reads a complete record from the reader.
func ReadRecord(r io.Reader) (*Record, error) {
	h, err := DecodeHeader(r)
	if err != nil {
		return nil, err
	}

	rec := &Record{Header: *h}

	// Read content
	if h.ContentLength > 0 {
		rec.Content = make([]byte, h.ContentLength)
		if _, err := io.ReadFull(r, rec.Content); err != nil {
			return nil, fmt.Errorf("read content: %w", err)
		}
	}

	// Discard padding
	if h.PaddingLength > 0 {
		pad := make([]byte, h.PaddingLength)
		if _, err := io.ReadFull(r, pad); err != nil {
			return nil, fmt.Errorf("read padding: %w", err)
		}
	}

	return rec, nil
}

// EncodeBeginRequest builds the 8-byte body for a FCGI_BEGIN_REQUEST record.
func EncodeBeginRequest(role uint16, flags uint8) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint16(b[0:2], role)
	b[2] = flags
	return b
}

// EncodeParam encodes a single FastCGI name-value pair.
func EncodeParam(name, value string) []byte {
	var buf bytes.Buffer
	encodeLength(&buf, len(name))
	encodeLength(&buf, len(value))
	buf.WriteString(name)
	buf.WriteString(value)
	return buf.Bytes()
}

// EncodeParams encodes multiple name-value pairs into a single byte slice.
func EncodeParams(params map[string]string) []byte {
	var buf bytes.Buffer
	for k, v := range params {
		encodeLength(&buf, len(k))
		encodeLength(&buf, len(v))
		buf.WriteString(k)
		buf.WriteString(v)
	}
	return buf.Bytes()
}

// DecodeParams decodes FastCGI name-value pairs from raw bytes.
func DecodeParams(data []byte) (map[string]string, error) {
	params := make(map[string]string)
	r := bytes.NewReader(data)

	for r.Len() > 0 {
		nameLen, err := decodeLength(r)
		if err != nil {
			return params, err
		}
		valueLen, err := decodeLength(r)
		if err != nil {
			return params, err
		}

		name := make([]byte, nameLen)
		if _, err := io.ReadFull(r, name); err != nil {
			return params, err
		}
		value := make([]byte, valueLen)
		if _, err := io.ReadFull(r, value); err != nil {
			return params, err
		}

		params[string(name)] = string(value)
	}

	return params, nil
}

func encodeLength(buf *bytes.Buffer, n int) {
	if n < 128 {
		buf.WriteByte(byte(n))
	} else {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(n)|0x80000000)
		buf.Write(b)
	}
}

func decodeLength(r io.ByteReader) (int, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	if b < 128 {
		return int(b), nil
	}
	// 4-byte encoding: high bit is flag, not part of length
	var buf [3]byte
	for i := range buf {
		buf[i], err = r.ReadByte()
		if err != nil {
			return 0, err
		}
	}
	n := uint32(b&0x7F)<<24 | uint32(buf[0])<<16 | uint32(buf[1])<<8 | uint32(buf[2])
	return int(n), nil
}
