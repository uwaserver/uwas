package fastcgi

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeDecodeHeader(t *testing.T) {
	h := &Header{
		Version:       version1,
		Type:          TypeStdout,
		RequestID:     1,
		ContentLength: 256,
		PaddingLength: 0,
	}

	var buf bytes.Buffer
	if err := EncodeHeader(&buf, h); err != nil {
		t.Fatal(err)
	}

	if buf.Len() != headerSize {
		t.Errorf("header size = %d, want %d", buf.Len(), headerSize)
	}

	decoded, err := DecodeHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Version != h.Version {
		t.Errorf("version = %d, want %d", decoded.Version, h.Version)
	}
	if decoded.Type != h.Type {
		t.Errorf("type = %d, want %d", decoded.Type, h.Type)
	}
	if decoded.RequestID != h.RequestID {
		t.Errorf("requestID = %d, want %d", decoded.RequestID, h.RequestID)
	}
	if decoded.ContentLength != h.ContentLength {
		t.Errorf("contentLength = %d, want %d", decoded.ContentLength, h.ContentLength)
	}
}

func TestWriteReadRecord(t *testing.T) {
	content := []byte("Hello FastCGI")
	var buf bytes.Buffer

	if err := WriteRecord(&buf, TypeStdout, 1, content); err != nil {
		t.Fatal(err)
	}

	rec, err := ReadRecord(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if rec.Type != TypeStdout {
		t.Errorf("type = %d, want %d", rec.Type, TypeStdout)
	}
	if !bytes.Equal(rec.Content, content) {
		t.Errorf("content = %q, want %q", rec.Content, content)
	}
}

func TestWriteReadEmptyRecord(t *testing.T) {
	var buf bytes.Buffer
	WriteRecord(&buf, TypeParams, 1, nil)

	rec, err := ReadRecord(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ContentLength != 0 {
		t.Errorf("contentLength = %d, want 0", rec.ContentLength)
	}
}

func TestEncodeDecodeParam(t *testing.T) {
	params := map[string]string{
		"SCRIPT_FILENAME": "/var/www/index.php",
		"REQUEST_URI":     "/test?foo=bar",
		"QUERY_STRING":    "foo=bar",
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

func TestEncodeSingleParam(t *testing.T) {
	encoded := EncodeParam("KEY", "VALUE")
	decoded, err := DecodeParams(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded["KEY"] != "VALUE" {
		t.Errorf("got %q, want VALUE", decoded["KEY"])
	}
}

func TestLongParamEncoding(t *testing.T) {
	// Test 4-byte length encoding (> 127 bytes)
	longValue := strings.Repeat("x", 200)
	encoded := EncodeParam("LONG", longValue)
	decoded, err := DecodeParams(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded["LONG"] != longValue {
		t.Errorf("long param length = %d, want %d", len(decoded["LONG"]), len(longValue))
	}
}

func TestBeginRequest(t *testing.T) {
	body := EncodeBeginRequest(RoleResponder, FlagKeepConn)
	if len(body) != 8 {
		t.Errorf("begin request body size = %d, want 8", len(body))
	}
	// Role should be in first 2 bytes big-endian
	role := uint16(body[0])<<8 | uint16(body[1])
	if role != RoleResponder {
		t.Errorf("role = %d, want %d", role, RoleResponder)
	}
	if body[2] != FlagKeepConn {
		t.Errorf("flags = %d, want %d", body[2], FlagKeepConn)
	}
}
