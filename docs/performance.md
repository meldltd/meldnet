# Diagnosing slow transfers

Managed client traffic follows `client A -> primary -> client B`, including when
both clients are on the same LAN. The primary decrypts and re-encrypts each
packet. A direct Mac-to-Mac test does not measure this path. Record the primary's
location, each client's connection type, transfer tool, file size, and rates with
units (MB/s is eight times Mbit/s).

Compare these paths in both directions using the same workload:

1. Each Mac to the Linux primary's ordinary network address.
2. Each Mac to the primary's VPN address.
3. Mac to Mac using VPN addresses.

A bulk single-stream TCP test separates tunnel throughput from file handling,
remote disk speed, and application encryption. If iperf3 is already available,
start `iperf3 -s -B ADDRESS` on the receiving machine, binding to the specific
address being tested. On the sender, run `iperf3 -c ADDRESS -t 20 -O 2`, then the
same command with `-R`. Stop the server afterwards. Only expose the listener to
the intended test sender; a public primary needs an appropriate access rule.
Do not compare a parallel-stream internet speed test with a single file transfer.

During each run, record primary CPU usage, interface errors/drops, round-trip
latency to the primary, and TCP retransmissions from the test output. Compare
primary-bound and relayed results: a slow primary-bound result points earlier
in the path than the client-to-client relay. A fast first path but slow second
path narrows investigation to the tunnel/host path. A slow third path alone
makes the primary relay or the other client's link the next focus. These are
investigation clues, not proof of a single cause.

## Repeatable isolated regression benchmark

`scripts/benchmark-throughput.sh` creates disposable Linux containers with a
primary and two enrolled clients. It measures direct underlay TCP, encrypted
client-to-primary TCP, and encrypted relayed TCP in both directions. Host IP
forwarding is disabled on the primary; daemons have no external tools on PATH.
No host tunnel, resolver, or route is changed. Docker, Go, and host jq are needed;
iperf3 is installed only in the disposable test image.

```
BENCH_SECONDS=10 BENCH_OUTPUT=bin/throughput scripts/benchmark-throughput.sh
```

Run without concurrent builds or other throughput tests. JSON output preserves
rates and retransmissions for comparison. The direct baseline is a virtual
bridge inside Docker, not a physical network: its rate is not a target for a
native Mac or a WAN. This validates real encrypted Linux traffic, but does not
validate native macOS/Windows performance or reproduce a particular deployment.

The primary relay preserves packet batches for WireGuard and Linux host writes.
It drains already queued packets without waiting to fill a batch. Its queue
remains bounded at 256 packets and can drop traffic under congestion. Batching
reduces per-packet overhead; it does not remove the primary from the data path.
