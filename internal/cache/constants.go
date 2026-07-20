package cache

// maxConcurrentWrites limits concurrent L2 (disk) and L3 (Redis) write
// goroutines. This prevents unbounded goroutine growth during cache
// population under high concurrency. The semaphore is shared across
// all cache tiers so a single slow backend cannot starve others.
const maxConcurrentWrites = 16
