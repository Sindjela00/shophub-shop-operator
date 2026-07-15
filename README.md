# Shop operator

Kubernetes operator (Go / Kubebuilder) that provides the CRDs used to deploy Shop
applications and their supporting resources.

## Structure

```
shophub-shop-operator/
├── PROJECT                      # kubebuilder project marker
├── go.mod
├── main.go                      # manager entrypoint
├── api/v1/                      # CRD types: Shop, DiscordChannel, Wallet
├── internal/controller/         # reconcilers for each CRD
├── config/
│   ├── crd/bases/                # generated CRD manifests
│   ├── rbac/                     # operator RBAC manifests
│   ├── manager/                  # manager Deployment manifest
│   ├── default/                  # kustomize entrypoint combining the above
│   └── samples/                  # example CRs
├── Dockerfile
└── .github/workflows/ci.yml
```

> This layout mirrors what `kubebuilder init` / `kubebuilder create api` would generate.
> Go and kubebuilder are not installed in the scaffolding environment, so these files are
> hand-authored placeholders — regenerate/fill them in once the toolchain is available.

## CRDs

- **Shop** — deploys the Shop app; `standard` availability = 2 replicas, `high` = 3 replicas
- **DiscordChannel** — provisions a Discord channel for a Shop's alert notifications
- **Wallet** — provisions the on-chain account that receives a Shop's customer payments

## Related repositories

- `shophub-app` — creates `Shop`/`DiscordChannel`/`Wallet` custom resources via its admin panel
- `shophub-shop` — the application deployed by the `Shop` CRD
- `shophub-helm-charts` — Helm chart that installs this operator and its CRDs
- `shophub-kube-state` — desired-state configuration for the Kubernetes cluster
