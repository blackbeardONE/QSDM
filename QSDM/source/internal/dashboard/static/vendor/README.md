# Vendored browser dependencies

These files are served from `/static/vendor/` and embedded into the validator
binary by `//go:embed static/*` in `internal/dashboard/dashboard.go`.

| File | Source | Version |
| --- | --- | --- |
| `three.module.min.js` | `https://cdn.jsdelivr.net/npm/three@0.170.0/build/three.module.min.js` | three r170 |
| `OrbitControls.js` | `https://cdn.jsdelivr.net/npm/three@0.170.0/examples/jsm/controls/OrbitControls.js` | three r170 |

`OrbitControls.js` carries one local modification: its bare `from 'three'`
import is rewritten to `from './three.module.min.js'`. That is the only edit —
re-apply it when bumping the version.

They are vendored rather than loaded from a CDN for two reasons:

1. An `<script type="importmap" src="...">` tag was previously used to map the
   bare `three` specifier to jsDelivr. External import maps are specified but
   not implemented in any shipping browser, so the 3D panels silently failed
   with `Failed to resolve module specifier "three"`.
2. Validator hosts frequently have no outbound internet access. Local files
   work regardless of egress rules.

Because nothing else loaded from a CDN, vendoring let the CSP in
`pkg/api/security.go` drop to `script-src 'self'` and `font-src 'self'`. That
is pinned by `TestSecurityHeaders_Baseline`: do not re-add a third-party origin
to fetch this library at runtime — bump the vendored copy instead.

## Upgrading

```bash
V=0.170.0
curl -sL -o three.module.min.js "https://cdn.jsdelivr.net/npm/three@$V/build/three.module.min.js"
curl -sL -o OrbitControls.js "https://cdn.jsdelivr.net/npm/three@$V/examples/jsm/controls/OrbitControls.js"
# then re-point the bare import
sed -i "s#} from 'three';#} from './three.module.min.js';#" OrbitControls.js
```
