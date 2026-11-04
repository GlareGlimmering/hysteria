Hysteria2 v2.13.0-hui.3 keeps the per-user outbound and fail-closed behavior
from v2.13.0-hui.2 and limits CertMagic DNS-01 propagation checks to the
explicit IPv4 resolvers `8.8.8.8:53` and `8.8.4.4:53`.

This is scoped only to ACME DNS-01 certificate issuance. It does not disable
IPv6 globally or change HY2 proxy traffic, resolver, routing, or authentication
behavior.
