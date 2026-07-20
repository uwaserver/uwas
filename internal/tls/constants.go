package uwastls

// onDemandMaxPerMinute limits ACME on-demand certificate issuance
// to at most this many requests per rolling 60-second window.
const onDemandMaxPerMinute = 10
