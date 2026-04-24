package proxy

import (
	"sync/atomic"
	"time"
)

// CircuitState represents the circuit breaker state.
type CircuitState int32

const (
	CircuitClosed   CircuitState = iota // normal operation
	CircuitOpen                         // failing, reject requests
	CircuitHalfOpen                     // testing recovery
)

// CircuitBreaker implements the circuit breaker pattern per backend.
type CircuitBreaker struct {
	threshold   int           // failures to trip open
	timeout     time.Duration // time before half-open
	lastFailure atomic.Int64  // UnixNano timestamp
	failures    atomic.Int32

	// state is accessed atomically; half-open probe is gated by a CAS on probeSlot.
	// probeSlot: 0 = no probe in flight, 1 = probe in flight
	state     atomic.Int32
	probeSlot atomic.Int32
}

// NewCircuitBreaker creates a circuit breaker.
func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 5
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &CircuitBreaker{
		threshold: threshold,
		timeout:   timeout,
	}
}

// Allow checks if a request should be allowed through.
func (cb *CircuitBreaker) Allow() bool {
	state := CircuitState(cb.state.Load())

	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(time.Unix(0, cb.lastFailure.Load())) >= cb.timeout {
			// Try to transition to half-open using CAS
			if cb.state.CompareAndSwap(int32(CircuitOpen), int32(CircuitHalfOpen)) {
				// Claim the probe slot for this first probe request
				cb.probeSlot.Store(1)
				return true
			}
			// Another goroutine won the transition; check if it's now half-open
			if CircuitState(cb.state.Load()) == CircuitHalfOpen {
				return cb.allowHalfOpenProbe()
			}
		}
		return false
	case CircuitHalfOpen:
		return cb.allowHalfOpenProbe()
	}
	return true
}

// allowHalfOpenProbe uses CAS to ensure only ONE probe request enters half-open at a time.
func (cb *CircuitBreaker) allowHalfOpenProbe() bool {
	// Try to claim the probe slot with CAS
	if cb.probeSlot.CompareAndSwap(0, 1) {
		return true
	}
	return false
}

// RecordSuccess records a successful request.
func (cb *CircuitBreaker) RecordSuccess() {
	if CircuitState(cb.state.Load()) == CircuitHalfOpen {
		cb.state.Store(int32(CircuitClosed))
		cb.failures.Store(0)
		cb.probeSlot.Store(0) // reset probe slot when closing from half-open
		return
	}
	cb.failures.Store(0)
}

// RecordFailure records a failed request.
func (cb *CircuitBreaker) RecordFailure() {
	cb.failures.Add(1)
	cb.lastFailure.Store(time.Now().UnixNano())

	if cb.failures.Load() >= int32(cb.threshold) {
		cb.state.Store(int32(CircuitOpen))
		cb.probeSlot.Store(0)
	}
}

// State returns the current state.
func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(cb.state.Load())
}
