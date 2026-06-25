---
title: UWAS FastCGI Package API
generated: true
---

# UWAS FastCGI Package API

<!-- Auto-generated from `go doc -all`. Do not edit manually. -->

```
package fastcgi // import "github.com/uwaserver/uwas/pkg/fastcgi"


CONSTANTS

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
    Record types.

const (
	RoleResponder  uint16 = 1
	RoleAuthorizer uint16 = 2
	RoleFilter     uint16 = 3
)
    Roles.

const (
	FlagKeepConn uint8 = 1
)
    Flags for BeginRequest.


FUNCTIONS

func DecodeParams(data []byte) (map[string]string, error)
    DecodeParams decodes FastCGI name-value pairs from raw bytes.

func EncodeBeginRequest(role uint16, flags uint8) []byte
    EncodeBeginRequest builds the 8-byte body for a FCGI_BEGIN_REQUEST record.

func EncodeHeader(w io.Writer, h *Header) error
    EncodeHeader writes an 8-byte header to the given writer.

func EncodeParam(name, value string) []byte
    EncodeParam encodes a single FastCGI name-value pair.

func EncodeParams(params map[string]string) []byte
    EncodeParams encodes multiple name-value pairs into a single byte slice.

func WriteRecord(w io.Writer, recType uint8, requestID uint16, content []byte) error
    WriteRecord writes a complete record (header + content + padding).


TYPES

type Client struct {
	// Has unexported fields.
}
    Client sends requests to a FastCGI server via a connection pool.

func NewClient(cfg PoolConfig) *Client
    NewClient creates a FastCGI client with the given pool config.

func (c *Client) Close()
    Close shuts down the client and its connection pool.

func (c *Client) Execute(ctx context.Context, env map[string]string, stdin io.Reader) (*Response, error)
    Execute sends a FastCGI request and returns the response.

type Header struct {
	Version       uint8
	Type          uint8
	RequestID     uint16
	ContentLength uint16
	PaddingLength uint8
	Reserved      uint8
}
    Header is an 8-byte FastCGI record header.

func DecodeHeader(r io.Reader) (*Header, error)
    DecodeHeader reads an 8-byte header from the given reader.

type Pool struct {
	// Has unexported fields.
}
    Pool manages a pool of FastCGI connections.

func NewPool(cfg PoolConfig) *Pool
    NewPool creates a new connection pool.

func (p *Pool) Close()
    Close drains and closes all connections. Idempotent.

func (p *Pool) Discard(c *conn)
    Discard closes a connection without returning it to the pool.

func (p *Pool) Get(ctx context.Context) (*conn, error)
    Get returns an idle connection or creates a new one.

func (p *Pool) Put(c *conn)
    Put returns a connection to the pool.

func (p *Pool) Stats() (active, idle int)
    Stats returns current pool statistics.

type PoolConfig struct {
	Address     string        // "unix:/var/run/php-fpm.sock" or "tcp:127.0.0.1:9000"
	MaxIdle     int           // max idle connections (default 10)
	MaxOpen     int           // max total connections (default 64)
	MaxLifetime time.Duration // max connection lifetime (default 5m)
}
    PoolConfig configures a connection pool.

type Record struct {
	Header
	Content []byte
}
    Record is a complete FastCGI record (header + content).

func ReadRecord(r io.Reader) (*Record, error)
    ReadRecord reads a complete record from the reader.

type Response struct {
	AppStatus uint32
	// Has unexported fields.
}
    Response holds the raw stdout/stderr output from FastCGI.

func (r *Response) ParseHTTP() (statusCode int, headers http.Header, body io.Reader)
    ParseHTTP parses the FastCGI response as HTTP (status, headers, body).
    PHP-FPM returns: "Status: 200 OK\r\nContent-Type: text/html\r\n\r\n<body>"

func (r *Response) Stderr() []byte
    Stderr returns the raw stderr output.

func (r *Response) Stdout() []byte
    Stdout returns the raw stdout output.

```
