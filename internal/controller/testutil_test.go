package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := shopv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add shopv1 to scheme: %v", err)
	}
	return scheme
}
