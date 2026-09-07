# Autoblock and the liveness watchdog

## What this is for

A live incident looked like this in the journal:

```
level=ERROR msg="http: TLS handshake error from 35.207.219.84:45854: EOF"
level=ERROR msg="http: TLS handshake error from 82.198.243.230:43815: EOF"
level=ERROR msg="http: TLS handshake error from 35.207.219.84:53218: EOF"
...
level=WARN  msg="domain health check" domain=dgn.plus status=down code=0 response_ms=10001
```

Two separate problems, and neither of them had a defence:

**1. The existing protections could not see the attack.** The WAF, the rate
limiter, the bot guard and the IP ACL are all HTTP middleware. They run after
TLS termination, on a parsed request. These connections were opened and dropped
before the client sent a ClientHello, so there was never a request for any of
them to inspect. Every connection still cost an accept, a goroutine, a file
descriptor and a handshake attempt — the flood was free to the attacker and
expensive to us.

**2. Nothing restarted the wedged process.** `Restart=on-failure` only reacts
to a process that exits. A server saturated at the accept path stays
`active (running)` and answers nothing, which is exactly what the `status=down`
line reports. Systemd had no way to tell the difference.

So: detection moved down to the accept path, and liveness became something the
process has to prove rather than something inferred from it still existing.

## Where each signal is seen

| Signal | Seen at | Catches |
|---|---|---|
| `conn_flood` | accept | connection rate from one source |
| `tls_abort` | connection close | **the handshake flood above** |
| `concurrent` | accept | slow-loris style connection hoarding |
| `waf` | request | repeated WAF / bot-guard hits |
| `rate` | request | repeated 429s |
| `notfound` | request | 404 sweeps (vulnerability scanning) |

Only the first three exist before TLS. `tls_abort` is the one that matches the
incident above: a connection that closes having never delivered a byte.
Browsers, bots, proxies and health checks all send at least a ClientHello — an
abort does not.

Enforcement runs in two layers. The in-memory block is immediate and free (one
map lookup per accepted connection, on a lock-free snapshot). With
`firewall_sync: true` the block is also pushed to ufw/iptables, so the kernel
refuses the SYN and the process is never woken at all — that is what actually
makes a large flood cheap to absorb.

## Configuration

```yaml
global:
  autoblock:
    enabled: true
    dry_run: true          # start here — see "Rolling it out" below
    window: 60s

    max_connections: 600   # new TCP connections per IP per window
    max_aborts: 60         # connections closed without sending a byte
    max_concurrent: 150    # simultaneous connections per IP (0 disables)

    max_waf_hits: 15
    max_rate_hits: 120
    max_not_found: 200

    block_duration: 15m
    escalate: true         # 15m -> 1h -> 4h -> ... capped below
    max_block_duration: 24h

    firewall_sync: true
    whitelist: []
    state_path: /var/lib/uwas/autoblock.json

  watchdog:
    enabled: true
    interval: 15s
    timeout: 5s            # must be shorter than interval
    failures: 3
    self_restart: false    # only for non-systemd supervisors
```

## What is never blocked

Applied unconditionally, whatever the config says:

- loopback, RFC1918 private, link-local and unspecified addresses
- everything in `global.trusted_proxies`
- everything in `global.cloudflare.ip_ranges`
- this host's own detected addresses
- anything in `autoblock.whitelist`

The Cloudflare entry is not a nicety. **Behind a CDN every connection carries an
edge IP.** A busy edge trips a connection threshold in seconds, and blocking one
takes the site offline for every visitor that edge serves. The whitelist is
refreshed on `SIGHUP` for exactly this reason: a freshly synced set of edge
ranges must not wait for a restart.

The same reasoning applies to `proxy_protocol: true`. Under PROXY protocol every
connection arrives from the load balancer, so connection-level counters would
all point at it. Connection-level detection is therefore **disabled** in that
mode (with a warning at startup) and only request-level signals apply — those
run after `RealIP` has resolved the true client. A handshake flood against a
PROXY-protocol deployment has to be stopped at the load balancer; UWAS never
sees the real source.

## Rolling it out

Do not enable enforcement blind. Thresholds that are wrong in the tight
direction take real visitors offline, which is worse than the attack.

1. Set `enabled: true` with `dry_run: true`. Reload (`systemctl reload uwas`).
2. Leave it for a day of representative traffic, then read what it would have done:
   ```bash
   journalctl -u uwas | grep 'autoblock (dry-run'
   ```
3. Anything in there that is a real visitor means a threshold is too tight, or
   an address belongs in the whitelist. Adjust and repeat.
4. Set `dry_run: false`.

`GET /api/v1/autoblock` reports live state and thresholds either way.

## API

```
GET    /api/v1/autoblock          # stats, thresholds and active blocks
POST   /api/v1/autoblock          # {"ip": "...", "reason": "...", "duration": "1h"|"permanent"}
DELETE /api/v1/autoblock/{ip}     # unblock, and clear the escalation history
```

`DELETE` clears escalation deliberately: an address an operator cleared by hand
should not come back at level 4 on its next offence.

## The watchdog

`Restart=` cannot see a hang. The watchdog makes one visible: UWAS probes its
own listener over loopback, and pings systemd's watchdog **only while the probe
passes**. When it stops passing for `failures` rounds in a row the ping is
withheld, `WatchdogSec` expires, systemd sends `SIGABRT`, and `Restart=always`
brings the service back.

Two details that matter:

- **Any HTTP status is a pass.** The probe tests whether accept → parse →
  handler → response still completes, not whether a route is correct. A `421`
  for an unconfigured Host is a healthy answer.
- **No connection reuse.** A pooled connection established while the server was
  healthy can keep answering from a kernel buffer. A fresh dial each time is
  what actually exercises the accept path — the path a flood saturates.

The restart decision stays with systemd, including its rate limiting.
`self_restart: true` is only for environments with no systemd watchdog (Docker,
a plain supervisor), where withholding a ping would achieve nothing.

### Unit requirements

The installer writes `/etc/systemd/system/uwas.service.d/10-resilience.conf` as
a drop-in rather than rewriting the unit, so an existing install keeps its
operator edits and still gets the change:

```ini
[Unit]
StartLimitIntervalSec=300
StartLimitBurst=10

[Service]
NotifyAccess=main
Restart=always
RestartSec=5
WatchdogSec=60
LimitNOFILE=1048576
StateDirectory=uwas
```

**`WatchdogSec` and `global.watchdog.enabled` must move together.** With
`WatchdogSec` set and the probe disabled, no ping is ever sent and systemd
restarts a perfectly healthy server every 60 seconds. The installer comments
`WatchdogSec` out when it cannot find a `watchdog:` block in `uwas.yaml`.

`StartLimitBurst` is not decoration either: systemd's default gives up
permanently after 5 restarts in 10 seconds, which converts a recoverable
overload into a lasting outage.

## Limits

- Autoblock is per-source-IP. A genuinely distributed flood from thousands of
  addresses, each staying under the thresholds, needs upstream mitigation — the
  in-memory block is cheap, but the packets still arrive.
- With `proxy_protocol: true`, connection-level detection is off (see above).
- Thresholds, durations and the window are read at startup; a reload refreshes
  the whitelist and `firewall_sync` only.
