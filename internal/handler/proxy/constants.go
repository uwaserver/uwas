package proxy

// maxRetryBodyBytes caps the request body size that can be buffered
// for retry on upstream failure. Bodies larger than 8MB are streamed
// directly without retry capability.
const maxRetryBodyBytes int64 = 8 << 20 // 8MB
