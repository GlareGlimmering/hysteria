# Hysteria2 v2.13.0-hui.2 PoC

This release is a functional proof of concept for per-authenticated-user
outbound selection. It is based on official Hysteria2 `app/v2.13.0` at commit
`ca8fbd874fd6ca82413948de6dc5924405e103e1`.

## Added

- Select one named outbound from the HTTP authenticator's exact returned `id`.
- Use the same immutable selection for every TCP request and UDP session on the
  authenticated HY2 connection.
- Configure bindings with `userOutbounds` and named server `outbounds`.
- Fail closed when an identity is unbound or its selected outbound fails.
- Include the selected outbound name in the extended TCP/UDP event log.

## Automated verification

- Separate users select separate TCP and UDP outbounds.
- An unbound authenticated identity is rejected.
- A failed selected outbound never calls the configured fallback outbound.
- A failed user's TCP/UDP outbound does not affect another user's selected
  outbound under concurrent execution.
- A loopback SOCKS5 integration test performs UDP ASSOCIATE and verifies the
  complete controlled UDP echo request and response path.
- Relevant tests are exercised with Go's race detector.

## Reported isolated-environment acceptance results

The following results were supplied from an isolated VPS PoC validation and
are recorded separately from automated test results:

- `test_direct` used DIRECT and observed exit IP `66.154.113.51`.
- `test_tm1` used `TM_NET_1` through local SOCKS5/Xray and observed exit IP
  `118.100.80.30`.
- `test_failure` remained on an unavailable `BROKEN_OUTBOUND`; TCP and observed
  UDP requests failed without an observed switch to DIRECT or `TM_NET_1`.
- `test_unbound` authenticated at the external authenticator but was rejected
  by the HY2 server because it had no `userOutbounds` binding.
- Disabling the upstream VLESS test identity stopped new `test_tm1` traffic
  without disrupting simultaneous `test_direct` traffic or producing an
  observed DIRECT fallback. Restoring it recovered the same TM_NET_1 exit.
- Concurrent devices retained their respective DIRECT and TM_NET_1 selections
  in the observed request logs.

## Compatibility and upgrade notes

- This is a separate custom binary and does not replace an official Hysteria2
  binary automatically.
- The existing authentication protocol and returned auth ID are unchanged.
- Configurations without `userOutbounds` retain the upstream single-outbound
  behavior. When `userOutbounds` is present, ACL and custom resolver composition
  are deliberately rejected by this PoC.
- Existing connections retain the outbound chosen at authentication time;
  reconnect clients after changing bindings.
- Use a separate UDP port and configuration for canary validation before any
  production consideration. See `HUI_POC.md` and `poc/config.example.yaml`.

## Security scope and known limitations

**Fail-Closed passed this PoC's functional tests, but this does not prove
absolute network-layer zero leakage under every exceptional condition.**

- End-to-end UDP reliability and comprehensive packet-level UDP leakage testing
  have not completed production acceptance. Observed UDP routing and targeted
  capture results are not a universal non-leak proof.
- This release has not received a production-grade security audit.
- The PoC uses static configuration and requires reconnecting clients to apply
  a changed binding.
- H-UI management integration and access-log consumption of the added outbound
  field remain outside this kernel PoC.
