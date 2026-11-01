# H-UI user-aware outbound PoC

This branch is a minimal proof of concept based on the unmodified upstream tag
`app/v2.13.0` at commit `ca8fbd874fd6ca82413948de6dc5924405e103e1`.

## Scope

- `userOutbounds` maps the HTTP authenticator's returned `id` to one named
  server outbound.
- Selection happens once after successful authentication and is reused for all
  TCP streams and UDP sessions on that QUIC connection.
- Missing users, missing outbound names, and failed selected outbounds are
  fail-closed. There is no fallback to the default or DIRECT outbound.
- The existing authentication response, auth ID, traffic logger, online state,
  and kick semantics are unchanged.
- Enhanced event log records contain an `outbound` field. Existing EventLogger
  implementations remain source-compatible through an optional extension.

This PoC deliberately does not integrate with the H-UI database or replace the
official Hysteria binary managed by H-UI.

## Reproducible verification

From the repository root with Go 1.26:

```bash
go test ./core/server ./extras/outbounds ./app/cmd
go test -race ./core/server ./extras/outbounds ./app/cmd
./poc/build-linux-amd64.sh
sha256sum dist/hysteria-linux-amd64-v2.13.0-hui.1
```

The build script embeds `v2.13.0-hui.1`, the exact Git commit, Linux/AMD64,
and disables symbol/debug output. The executable itself does not replace any
H-UI file.

## Isolated VPS canary

1. Keep production H-UI, HY2 UDP 443, Xray, firewall, and subscriptions
   unchanged.
2. Copy the PoC binary to `/var/lib/h-ui/hy2-poc/bin/` and the example config
   to `/var/lib/h-ui/hy2-poc/config.yaml`.
3. Provide separate test TLS files and a separate HTTP authentication endpoint
   that returns the exact IDs `test_direct`, `test_tm1`, and `test_unbound`.
4. Change the example listen port only if `24443/udp` conflicts. Do not reuse
   UDP 443. Bind traffic stats to loopback.
5. Start manually in a dedicated terminal or a dedicated
   `h-ui-hysteria-poc.service`; do not point the production H-UI service at it:

```bash
HYSTERIA_DISABLE_UPDATE_CHECK=1 \
HYSTERIA_LOG_LEVEL=debug \
/var/lib/h-ui/hy2-poc/bin/hysteria-linux-amd64-v2.13.0-hui.1 \
  server -c /var/lib/h-ui/hy2-poc/config.yaml
```

6. Create three test client profiles for the canary UDP port. Confirm:
   - `test_direct`: an HTTPS IP endpoint sees the VPS public IP.
   - `test_tm1`: the same endpoint sees `118.100.80.30`.
   - `test_unbound`: authentication is rejected.
7. For UDP, use a controlled UDP echo service that returns the observed source
   address. A DNS reply alone is not exit identity verification.
8. Stop Xray or temporarily point the PoC TM_NET_1 entry at an unused loopback
   port. `test_tm1` must fail while `test_direct` remains usable. Verify the IP
   endpoint never sees the VPS address for `test_tm1`.
9. Inspect debug JSON logs and confirm each TCP/UDP request contains the correct
   `id` and `outbound`.

## Rollback

Stop and disable only the dedicated PoC process/service, then remove its
isolated directory and canary firewall allowance if one was manually added.
No production H-UI/HY2/Xray process, binary, configuration, database, or
subscription needs to be changed or restarted.

## Known limitations

- This is static startup configuration; changing a binding requires a canary
  restart and reconnect. Existing connections retain their selected outbound.
- User routing is intentionally incompatible with ACL and custom resolvers in
  this PoC.
- Real TM_NET_1 TCP/UDP and exit identity require isolated VPS validation.
- H-UI's current access-log parser does not yet consume the new `outbound`
  field; this PoC only emits it from the custom kernel.
- No H-UI UI, persistence, candidate apply, or rollback integration exists.
