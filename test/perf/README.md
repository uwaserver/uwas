# Response-time benchmark environment

Two layers, used for two different questions.

| Layer | Question it answers | Cost per run |
|---|---|---|
| `go test -bench HotPath ./internal/server` | *Where does the CPU go, and what does each subsystem cost?* | seconds |
| `test/perf/ab.sh` | *Did the change actually make the server answer faster over a socket?* | minutes |

Start with layer 1 — it is fast, low-noise, and profileable. Confirm with layer 2.

## Layer 1 — in-process hot path

`internal/server/hotpath_bench_test.go` drives the real middleware chain
(`s.handler`) with an allocation-free `ResponseWriter`: no kernel, no socket,
no client stealing CPU from the server.

```bash
go test ./internal/server -run '^$' -bench HotPath -benchmem -count 10 > new.txt
benchstat old.txt new.txt          # go install golang.org/x/perf/cmd/benchstat@latest
```

The `NoAnalytics` / `NoAdminLog` / `NoBandwidth` / `NoAlerter` / `Bare`
variants each switch off exactly one per-request bookkeeping subsystem.
`baseline − variant` is what that subsystem costs. `Bare` is all of them at
once: the ceiling on what tuning that area can ever buy.

To see where the time really goes:

```bash
go test ./internal/server -run '^$' -bench 'HotPathStatic$' -cpuprofile cpu.out -o server.test
go tool pprof -top -cum -nodecount=40 server.test cpu.out
```

## Layer 2 — over a real socket

```bash
go build -o bin/uwas ./cmd/uwas
test/perf/bench.sh                          # static.perf, closed loop, 50 conns, 10s
test/perf/bench.sh -H cached.perf -r 30000  # cached vhost, open loop at 30k req/s
```

`bench.sh` boots the binary against `uwas-perf.yaml`, waits for readiness,
runs `loadgen`, and always kills the server on the way out. It prints JSON.

Three vhosts on one server, so you can price a feature without restarting:
`static.perf` (uncached), `cached.perf` (L1 memory cache), `gzip.perf`
(compression on). Payloads in `www/` are fixed at 1 KB, 16 KB and 256 KB.

### Comparing two builds

```bash
test/perf/baseline.sh HEAD              # -> bin/uwas-base, built in a throwaway worktree
go build -o bin/uwas ./cmd/uwas         # your change
test/perf/ab.sh -n 5 bin/uwas-base bin/uwas
```

Rounds alternate A,B,A,B,… and each side reports the median of its rounds.
Interleaving is not fussiness: thermal drift and background processes move
in one direction over a few minutes, and running all of A then all of B
hands that drift to whichever side went second.

**Noise floor.** Running the harness against two copies of the same binary
gives ≤1.5% on rps/p50/p99 over 3 rounds. Treat anything under ~3% as a tie.

## Closed loop vs open loop

`-rate 0` (default) is closed loop: each connection sends again as soon as the
last reply lands. It finds peak throughput, but it *hides* latency — when the
server slows down the client politely sends less, so the queue never builds.
That is coordinated omission.

`-rate N` is open loop: requests are scheduled at N/sec no matter how the
server is doing, and latency is measured from the **scheduled** send time.
Use it, below saturation, whenever the question is "how fast does it answer".

## Reading the numbers

- The load generator shares the machine with the server. On a 10-core laptop
  the client can eat a third of the CPU, which compresses every difference.
  Ratios stay meaningful; absolute rps is a lower bound.
- macOS syscalls (`open`, `stat`) are markedly more expensive than Linux's.
  A filesystem-bound finding here is real on Linux but smaller. Confirm any
  syscall-related win on the target OS before sizing it.
- `-count 10` plus `benchstat` for layer 1; `-n 5` for layer 2. One run of
  either is an anecdote.
