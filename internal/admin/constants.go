package admin

import "time"

// maxLogEntries sets the capacity of the admin audit-log ring buffer.
// Older entries are silently discarded once the buffer is full.
const maxLogEntries = 1000

// listeningProbeTimeout is how long Create/Update/Start handlers wait
// for the app's port to become connectable. On timeout they report a
// listening_warning but still succeed — the process may still be warming up.
const listeningProbeTimeout = 15 * time.Second

// deployListeningProbeTimeout is how long post-deploy restart waits for
// the port before treating the deploy as failed and rolling back.
// Node/npm apps (and similar) routinely need more than a few seconds after
// Start; the old 3s window caused false rollbacks (e.g. crm on :3002).
const deployListeningProbeTimeout = 45 * time.Second
