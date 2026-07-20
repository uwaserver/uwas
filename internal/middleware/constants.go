package middleware

// maxRecentBlocked is the capacity of the SecurityStats ring buffer
// for recent blocked-request records. Older entries wrap around.
const maxRecentBlocked = 200
