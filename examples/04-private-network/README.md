# Test a private network with VLatte

Run VLatte as a known service behind a mesh VPN or zero-trust network tool. Check that an access policy allows, blocks and restores access to it, then see how a caller copes with a stalled response, a slow download and a truncated one. The walkthrough uses [NetBird](https://netbird.io). The same steps apply to any tool that controls who reaches a TCP port, such as Tailscale ACLs or a host firewall on a WireGuard interface.

Run the commands from the repository root, with a checkout on each machine. You need Go, Make, Bash and curl, plus `jq` on the machine that runs VLatte. The mock service listens on TCP `8080`, and its admin API on `127.0.0.1:8081`.

## Run on loopback

```sh
make example:private-network                                            # terminal 1
bash examples/04-private-network/probe.sh faults http://127.0.0.1:8080  # terminal 2
```

Every probe prints a `journal` command with a unique ID. Run it where VLatte runs. The probe shows what came back to the caller, and the journal shows what reached VLatte.

## Check an access policy with NetBird

Use two connected peers in a disposable NetBird network. Put one in group `lab-caller` and the other in `lab-backend`. Create a policy from `lab-caller` to `lab-backend` for TCP `8080`, and disable the default all-to-all policy and any other policy that grants that access ([NetBird access policies](https://docs.netbird.io/manage/access-control/manage-network-access)).

Start VLatte on the backend's NetBird address. On the caller, point the probes at that address:

```sh
make example:private-network LAB_ADDR=100.64.0.10:8080  # backend
backend=http://100.64.0.10:8080                         # caller
```

After each probe, run the `journal` command it prints on the backend.

1. **Allow:** `probe.sh allow "$backend"`. `/healthz` returns `200`, and the journal shows one arrival.
2. **Deny:** disable the policy, then run `probe.sh deny "$backend"`. The connection fails or times out, and the journal shows zero arrivals.
3. **Restore:** re-enable the policy, then run `probe.sh restore "$backend"`. Access and the journal arrival come back.
4. **Faults:** `probe.sh faults "$backend"` runs the fault probes through the tunnel.

A policy change can take a moment to reach the peers, so retry if a probe fails. Each run uses a new ID. With another tool, bind VLatte to its interface address and toggle its rule instead.

## Fault routes

| Route | Fault | Caller sees |
|---|---|---|
| `/stall` | `timeout` in `wire_pipes`, cut off after 5s | No headers before curl's 2-second deadline |
| `/download/slow` | `bandwidth` in `pipes`, 260 B/s | The full 520-byte payload, after about 2s |
| `/download/truncated` | `limit_data` in `pipes`, 64 B | `200`, then a partial transfer of 64 bytes |

`/healthz` must answer before the faults and after each one. The probes use HTTP/1.1 because `wire_pipes` acts on the whole response, status line and headers included. VLatte injects these faults; the tunnel itself sees no packet loss.

## NetBird extra: encrypted Management Login

```sh
make example:private-network-test
```

NetBird's control plane encrypts every gRPC payload. In [login_test.go](management/login_test.go), a `mockgrpc.RequestParser` ([login.go](management/login.go)) decrypts each `EncryptedMessage`, so `Field("setupKey", ...)` can match on the plain request. The mock encrypts its reply for the peer. The test also checks that a different setup key misses and that a tampered ciphertext gets `InvalidArgument`. Use the same pattern for any tool whose protocol wraps its messages. The test runs in memory, with no NetBird deployment.
