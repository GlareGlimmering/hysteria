# Upstream and patch record

- Upstream repository: `https://github.com/apernet/hysteria.git`
- Upstream tag: `app/v2.13.0`
- Annotated tag object: `7fa51f40bf657d62276fd525ed6dacad7a4eee5d`
- Upstream commit: `ca8fbd874fd6ca82413948de6dc5924405e103e1`
- Custom version: `v2.13.0-hui.1`
- Development branch: `feature/hui-user-outbound-poc`

Generate a standalone patch after the PoC commit with:

```bash
git format-patch --stdout app/v2.13.0..feature/hui-user-outbound-poc \
  > poc/hysteria-v2.13.0-hui.1.patch
```
