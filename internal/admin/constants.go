package admin

import "time"

// maxLogEntries sets the capacity of the admin audit-log ring buffer.
// Older entries are silently discarded once the buffer is full.
const maxLogEntries = 1000

// listeningProbeTimeout is how long Create/Update/Start handlers wait
// for the app's port to become connectable before reporting "started
// but not yet listening". 3s catches normal startup; longer-warming
// apps trip the warning but don't fail the deploy.
const listeningProbeTimeout = 3 * time.Second
