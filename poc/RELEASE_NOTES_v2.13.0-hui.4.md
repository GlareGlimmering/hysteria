Mixed-case user identity routing fix (test prerelease).

Viper lowercased usernames in userOutbounds while HTTP authentication returned
the original ID. For example TESTsss became testsss, so outbound selection
rejected an otherwise authenticated connection and invoked masquerade.

Read the identity map from the original YAML/JSON to preserve exact IDs.
Case-distinct users remain distinct; unbound users remain rejected. Other
configuration fields still use the existing Viper loader. userOutbounds now
requires a YAML or JSON configuration file; other formats with this custom
extension are explicitly rejected rather than changing identity case.

Regression coverage: file parsing and DIRECT selection for TESTsss/testsss,
HTTP callback identity preservation, and HTTP/3 TCP/UDP echo for mixed-case
users. Existing per-user isolation and fail-closed tests remain enabled.

This fixes the reproduced configuration parsing defect. Shadowrocket speed
testing and the graywind-xyxq-01 server have not been tested or deployed by CI.
ACME IPv4 behavior from hui.3 is retained. No h-ui version or pin is changed.
