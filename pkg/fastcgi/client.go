package fastcgi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"time"
)

// Client sends requests to a FastCGI server via a connection pool.
type Client struct {
	pool *Pool
}

// NewClient creates a FastCGI client with the given pool config.
func NewClient(cfg PoolConfig) *Client {
	return &Client{
		pool: NewPool(cfg),
	}
}

// Response holds the raw stdout/stderr output from FastCGI.
type Response struct {
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	AppStatus uint32
}

// Execute sends a FastCGI request and returns the response.
func (c *Client) Execute(ctx context.Context, env map[string]string, stdin io.Reader) (*Response, error) {
	cn, err := c.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}

	// Set read/write deadline to prevent hanging forever
	deadline := time.Now().Add(60 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	cn.netConn.SetDeadline(deadline)

	// Use a buffered writer for efficiency
	bw := bufio.NewWriter(cn.netConn)
	requestID := uint16(1)

	// 1. FCGI_BEGIN_REQUEST
	beginBody := EncodeBeginRequest(RoleResponder, FlagKeepConn)
	if err := WriteRecord(bw, TypeBeginRequest, requestID, beginBody); err != nil {
		c.pool.Discard(cn)
		return nil, fmt.Errorf("write begin: %w", err)
	}

	// 2. FCGI_PARAMS
	params := EncodeParams(env)
	if err := WriteRecord(bw, TypeParams, requestID, params); err != nil {
		c.pool.Discard(cn)
		return nil, fmt.Errorf("write params: %w", err)
	}
	// Empty params record signals end of params
	if err := WriteRecord(bw, TypeParams, requestID, nil); err != nil {
		c.pool.Discard(cn)
		return nil, fmt.Errorf("write params end: %w", err)
	}

	// 3. FCGI_STDIN (request body)
	if stdin != nil {
		buf := make([]byte, maxContentLength)
		for {
			n, readErr := stdin.Read(buf)
			if n > 0 {
				if err := WriteRecord(bw, TypeStdin, requestID, buf[:n]); err != nil {
					c.pool.Discard(cn)
					return nil, fmt.Errorf("write stdin: %w", err)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				c.pool.Discard(cn)
				return nil, fmt.Errorf("read stdin: %w", readErr)
			}
		}
	}
	// Empty stdin record signals end of input
	if err := WriteRecord(bw, TypeStdin, requestID, nil); err != nil {
		c.pool.Discard(cn)
		return nil, fmt.Errorf("write stdin end: %w", err)
	}

	// Flush all buffered writes
	if err := bw.Flush(); err != nil {
		c.pool.Discard(cn)
		return nil, fmt.Errorf("flush: %w", err)
	}

	// 4. Read response records
	resp := &Response{}
	br := bufio.NewReader(cn.netConn)

	for {
		rec, err := ReadRecord(br)
		if err != nil {
			c.pool.Discard(cn)
			return nil, fmt.Errorf("read record: %w", err)
		}

		switch rec.Type {
		case TypeStdout:
			if len(rec.Content) > 0 {
				resp.stdout.Write(rec.Content)
			}
		case TypeStderr:
			if len(rec.Content) > 0 {
				resp.stderr.Write(rec.Content)
			}
		case TypeEndRequest:
			if len(rec.Content) >= 4 {
				resp.AppStatus = binary.BigEndian.Uint32(rec.Content[0:4])
			}
			c.pool.Put(cn)
			return resp, nil
		}
	}
}

// Close shuts down the client and its connection pool.
func (c *Client) Close() {
	c.pool.Close()
}

// Stdout returns the raw stdout output.
func (r *Response) Stdout() []byte {
	return r.stdout.Bytes()
}

// Stderr returns the raw stderr output.
func (r *Response) Stderr() []byte {
	return r.stderr.Bytes()
}

// ParseHTTP parses the FastCGI response as HTTP (status, headers, body).
// PHP-FPM returns: "Status: 200 OK\r\nContent-Type: text/html\r\n\r\n<body>"
func (r *Response) ParseHTTP() (statusCode int, headers http.Header, body io.Reader) {
	reader := bufio.NewReader(&r.stdout)
	tp := textproto.NewReader(reader)

	mimeHeader, err := tp.ReadMIMEHeader()
	if err != nil {
		// If header parsing fails, return raw stdout as body
		return http.StatusOK, http.Header{}, &r.stdout
	}

	headers = http.Header(mimeHeader)

	// Parse Status header
	if status := headers.Get("Status"); status != "" {
		if len(status) >= 3 {
			if code, err := strconv.Atoi(status[:3]); err == nil {
				statusCode = code
			}
		}
		headers.Del("Status")
	}
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	// If Location header is present but status is 200 (no Status header from PHP),
	// upgrade to 302. PHP-FPM sometimes sends Location without explicit Status header
	// (e.g. wp_redirect uses header("Location: ...") which PHP-CGI wraps as a header
	// but may not include "Status: 302" depending on SAPI behavior).
	// Without this, browsers receive 200 + Location and show a blank page.
	if headers.Get("Location") != "" && statusCode == http.StatusOK {
		statusCode = http.StatusFound // 302
	}

	body = reader // remaining bytes = response body
	return
}
