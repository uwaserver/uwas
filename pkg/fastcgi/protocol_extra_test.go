package fastcgi

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestWriteRecordWithPadding tests records that result in padding.
func TestWriteRecordWithPadding(t *testing.T) {
	// Content length not aligned to 8 bytes
	content := []byte("abc") // 3 bytes, needs 5 bytes padding to align to 8
	var buf bytes.Buffer

	if err := WriteRecord(&buf, TypeStdout, 1, content); err != nil {
		t.Fatal(err)
	}

	rec, err := ReadRecord(&buf)
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if !bytes.Equal(rec.Content, content) {
		t.Errorf("content = %q, want %q", rec.Content, content)
	}
}

// TestWriteRecordLargeContent tests writing a record near the max content length.
func TestWriteRecordLargeContent(t *testing.T) {
	content := bytes.Repeat([]byte("A"), 60000)
	var buf bytes.Buffer

	if err := WriteRecord(&buf, TypeStdout, 1, content); err != nil {
		t.Fatal(err)
	}

	rec, err := ReadRecord(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Content) != 60000 {
		t.Errorf("content length = %d, want 60000", len(rec.Content))
	}
}

// TestDecodeParamsLongKeyAndValue tests decoding params with both key and value > 127 bytes.
func TestDecodeParamsLongKeyAndValue(t *testing.T) {
	longKey := strings.Repeat("K", 200)
	longVal := strings.Repeat("V", 300)

	encoded := EncodeParam(longKey, longVal)
	decoded, err := DecodeParams(encoded)
	if err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	if decoded[longKey] != longVal {
		t.Errorf("long key/value decode mismatch: key len=%d, val len=%d", len(decoded[longKey]), len(longVal))
	}
}

// TestDecodeParamsEmpty tests decoding an empty params byte slice.
func TestDecodeParamsEmpty(t *testing.T) {
	decoded, err := DecodeParams([]byte{})
	if err != nil {
		t.Fatalf("DecodeParams empty: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("got %d params, want 0", len(decoded))
	}
}

// TestDecodeParamsTruncated tests that truncated data returns an error.
func TestDecodeParamsTruncated(t *testing.T) {
	// Encode a valid param, then truncate
	encoded := EncodeParam("KEY", "VALUE")
	truncated := encoded[:len(encoded)-3]

	_, err := DecodeParams(truncated)
	if err == nil {
		t.Error("expected error for truncated params")
	}
}

// TestDecodeHeaderTruncated tests reading a header from insufficient data.
func TestDecodeHeaderTruncated(t *testing.T) {
	// Only 4 bytes when 8 are needed
	buf := bytes.NewReader([]byte{1, 6, 0, 1})
	_, err := DecodeHeader(buf)
	if err == nil {
		t.Error("expected error for truncated header")
	}
}

// TestReadRecordTruncatedContent tests reading a record where content is shorter than declared.
func TestReadRecordTruncatedContent(t *testing.T) {
	// Write a valid header declaring 10 bytes of content but only provide 3
	var buf bytes.Buffer
	h := &Header{
		Version:       version1,
		Type:          TypeStdout,
		RequestID:     1,
		ContentLength: 10,
		PaddingLength: 0,
	}
	EncodeHeader(&buf, h)
	buf.Write([]byte("abc")) // only 3 bytes instead of 10

	_, err := ReadRecord(&buf)
	if err == nil {
		t.Error("expected error for truncated content")
	}
}

// TestEncodeParamsMultiple tests encoding multiple params at once.
func TestEncodeParamsMultiple(t *testing.T) {
	params := map[string]string{
		"A":   "1",
		"BB":  "22",
		"CCC": strings.Repeat("c", 150), // long value
	}

	encoded := EncodeParams(params)
	decoded, err := DecodeParams(encoded)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range params {
		if decoded[k] != v {
			t.Errorf("param %q = %q, want %q", k, decoded[k], v)
		}
	}
}

// TestWriteRecordError tests WriteRecord with a failing writer.
func TestWriteRecordError(t *testing.T) {
	w := &failWriter{failAfter: 0}
	err := WriteRecord(w, TypeStdout, 1, []byte("hello"))
	if err == nil {
		t.Error("expected error from failing writer")
	}
}

// TestWriteRecordContentWriteError tests WriteRecord failing during content write.
func TestWriteRecordContentWriteError(t *testing.T) {
	w := &failWriter{failAfter: 8} // header succeeds (8 bytes), content fails
	err := WriteRecord(w, TypeStdout, 1, []byte("hello"))
	if err == nil {
		t.Error("expected error during content write")
	}
}

// TestWriteRecordPaddingWriteError tests WriteRecord failing during padding write.
func TestWriteRecordPaddingWriteError(t *testing.T) {
	w := &failWriter{failAfter: 13} // header(8) + content(5) succeeds, padding fails
	err := WriteRecord(w, TypeStdout, 1, []byte("hello"))
	if err == nil {
		t.Error("expected error during padding write")
	}
}

type failWriter struct {
	written   int
	failAfter int
}

func (w *failWriter) Write(p []byte) (int, error) {
	if w.written+len(p) > w.failAfter {
		remaining := w.failAfter - w.written
		if remaining <= 0 {
			return 0, errors.New("write error")
		}
		w.written += remaining
		return remaining, errors.New("write error")
	}
	w.written += len(p)
	return len(p), nil
}

// TestReadRecordPaddingError tests ReadRecord failing during padding discard.
func TestReadRecordPaddingError(t *testing.T) {
	// Create a record with padding, but truncate the padding bytes
	var buf bytes.Buffer
	h := &Header{
		Version:       version1,
		Type:          TypeStdout,
		RequestID:     1,
		ContentLength: 5,
		PaddingLength: 3,
	}
	EncodeHeader(&buf, h)
	buf.Write([]byte("hello"))
	// Don't write the 3 padding bytes

	_, err := ReadRecord(&buf)
	if err == nil {
		t.Error("expected error for missing padding")
	}
}

// TestDecodeParamsTruncated4ByteLength tests truncated 4-byte length encoding.
func TestDecodeParamsTruncated4ByteLength(t *testing.T) {
	// Build data with a 4-byte length prefix (high bit set) but truncate it
	// A byte >= 0x80 signals 4-byte encoding, so we need 3 more bytes
	data := []byte{0x80, 0x00} // only 2 bytes when 4 are needed
	_, err := DecodeParams(data)
	if err == nil {
		t.Error("expected error for truncated 4-byte length")
	}
}

// TestDecodeParamsTruncatedValueLength tests params with valid name length but truncated value.
func TestDecodeParamsTruncatedValueLength(t *testing.T) {
	// Valid name length (1 byte = 3), valid value length (1 byte = 5)
	// but only provide 3 bytes for name and 2 for value
	data := []byte{3, 5, 'K', 'E', 'Y', 'V', 'A'} // value needs 5 bytes but only 2 here
	_, err := DecodeParams(data)
	if err == nil {
		t.Error("expected error for truncated value data")
	}
}

// TestDecodeParamsValueLengthTruncated tests when the value length byte itself is missing.
func TestDecodeParamsValueLengthTruncated(t *testing.T) {
	// Valid name length (1 byte = 3), but no value length byte
	data := []byte{3} // only name length, no value length
	_, err := DecodeParams(data)
	if err == nil {
		t.Error("expected error for missing value length")
	}
}

// TestEncodeParamShortNameLongValue tests 1-byte name, 4-byte value encoding.
func TestEncodeParamShortNameLongValue(t *testing.T) {
	name := "X"
	value := strings.Repeat("v", 200) // > 127 bytes

	encoded := EncodeParam(name, value)
	decoded, err := DecodeParams(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded[name] != value {
		t.Errorf("short name long value: got len=%d, want len=%d", len(decoded[name]), len(value))
	}
}
