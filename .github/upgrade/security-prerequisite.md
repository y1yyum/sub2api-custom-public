# Security prerequisite: GO-2026-6860

This is a narrow, explicitly approved deviation from the fixed v0.2.18 content
transplant, discovered by the existing CI vulnerability gate. It does not change
the target upstream revision, application behavior or the Go 1.27.3 toolchain.

The [official advisory](https://pkg.go.dev/vuln/GO-2026-6860)
([machine-readable Go vulnerability database entry](https://vuln.go.dev/ID/GO-2026-6860.json))
was published on 2026-10-27 and identifies CVE-2026-78661: adjacent text-node
coalescing in `golang.org/x/net/html` can cause quadratic allocations and copying.
It affects `html.Parse` and related parsing entry points before v0.62.0.
This application calls `html.Parse` in
`backend/internal/service/ollama_cloud_usage_parser.go:parseOllamaCloudUsageHTML`;
CI reported the reachable call path rather than merely an unused module.

Run with the existing Go 1.27.3 toolchain:

```sh
cd backend
go get golang.org/x/net@v0.62.0
go mod verify
```

The resulting minimal Go module-selection closure is:

| Module | Before | After | Why required |
| --- | --- | --- | --- |
| golang.org/x/net | v0.60.0 | v0.62.0 | First fixed version |
| golang.org/x/crypto | v0.57.0 | v0.58.0 | x/net v0.62.0 requires it |
| golang.org/x/sys | v0.48.0 | v0.49.0 | x/net v0.62.0 requires it |
| golang.org/x/term | v0.46.0 | v0.47.0 | x/net v0.62.0 requires it |
| golang.org/x/text | v0.42.0 | v0.43.0 | x/net v0.62.0 requires it |
| golang.org/x/tools | v0.50.0 | v0.51.0 | x/text v0.43.0 requires it |
| golang.org/x/sync | v0.23.0 | v0.24.0 | x/text v0.43.0 requires it |

No `-u`, replacement, advisory suppression or scanner bypass is used. The existing
`govulncheck@v1.9.0` gate remains unchanged. Old `go.sum` entries are retained;
they are integrity records, not proof that an old module version remains selected.
Verify selected versions with `go list -m all` and edges with `go mod graph`.

Module checksum verification and focused parser tests do not replace the full
application build/tests and symbol-level `govulncheck ./...`. Those gates must
run on the final commit; see actual local/CI results rather than treating this
note as evidence that every gate passed.
