# Autoblock / rate-limit lab

Local harness that:

1. **Synthetically** drives the autoblock `Blocker` with TEST-NET peer IPs
   (loopback is always Safe, so live `curl` cannot trip `conn_flood` /
   `concurrent` against itself).
2. **Live**-starts `bin/uwas` on `127.0.0.1:18080` and verifies domain rate
   limit 429s + WAF.

```bash
./test/autoblock-lab/run.sh
# or
go run ./test/autoblock-lab -synth-only

# Reproduce connection-reset hang class (autoblock concurrent trip):
go run ./test/autoblock-lab/reprohang
```

Report: `test/autoblock-lab/last-report.json` (generated; not committed)
