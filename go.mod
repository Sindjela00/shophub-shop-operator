module github.com/shophub/shophub-shop-operator

go 1.22

// Exact versions here are best-effort hand-picks (Go/kubebuilder aren't installed in the
// scaffolding environment to run `go mod tidy`) — run that once the toolchain is available
// to get a real, resolved go.sum.
require (
	k8s.io/apimachinery v0.31.0
	sigs.k8s.io/controller-runtime v0.19.0
)
