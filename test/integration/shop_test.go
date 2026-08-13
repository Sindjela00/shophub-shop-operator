package integration_test

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
)

var cnpgClusterGVK = schema.GroupVersionKind{Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster"}
var redisGVK = schema.GroupVersionKind{Group: "redis.redis.opstreelabs.in", Version: "v1beta2", Kind: "Redis"}

func TestShopReconciliation_standardTierDeploysTwoReplicasAndACNPGCluster(t *testing.T) {
	ctx := context.Background()
	shop := &shopv1.Shop{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-it-standard", Namespace: "default"},
		Spec: shopv1.ShopSpec{
			Name:          "IT Standard Shop",
			Availability:  shopv1.ShopAvailabilityStandard,
			WalletAddress: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
			DatabaseKind:  shopv1.ShopDatabaseKindStandard,
		},
	}
	if err := k8sClient.Create(ctx, shop); err != nil {
		t.Fatalf("create shop: %v", err)
	}

	var deployment appsv1.Deployment
	eventually(t, 10*time.Second, func() bool {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(shop), &deployment) == nil
	})
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 2 {
		t.Errorf("replicas = %v, want 2", deployment.Spec.Replicas)
	}
	if len(deployment.GetOwnerReferences()) != 1 || deployment.GetOwnerReferences()[0].Kind != "Shop" {
		t.Errorf("deployment owner references = %+v, want a single Shop owner", deployment.GetOwnerReferences())
	}

	var svc corev1.Service
	eventually(t, 5*time.Second, func() bool {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(shop), &svc) == nil
	})

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(cnpgClusterGVK)
	dbKey := client.ObjectKey{Name: shop.Name + "-db", Namespace: shop.Namespace}
	eventually(t, 5*time.Second, func() bool {
		return k8sClient.Get(ctx, dbKey, cluster) == nil
	})
	instances, found, err := unstructured.NestedInt64(cluster.Object, "spec", "instances")
	if err != nil || !found || instances != 1 {
		t.Errorf("CNPG cluster spec.instances = %v (found=%v err=%v), want 1", instances, found, err)
	}
}

func TestShopReconciliation_lightTierDeploysThreeReplicasAndRedisNotCNPG(t *testing.T) {
	ctx := context.Background()
	shop := &shopv1.Shop{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-it-light", Namespace: "default"},
		Spec: shopv1.ShopSpec{
			Name:          "IT Light Shop",
			Availability:  shopv1.ShopAvailabilityHigh,
			WalletAddress: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
			DatabaseKind:  shopv1.ShopDatabaseKindLight,
		},
	}
	if err := k8sClient.Create(ctx, shop); err != nil {
		t.Fatalf("create shop: %v", err)
	}

	var deployment appsv1.Deployment
	eventually(t, 10*time.Second, func() bool {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(shop), &deployment) == nil
	})
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 3 {
		t.Errorf("replicas = %v, want 3", deployment.Spec.Replicas)
	}

	dbKey := client.ObjectKey{Name: shop.Name + "-db", Namespace: shop.Namespace}

	redis := &unstructured.Unstructured{}
	redis.SetGroupVersionKind(redisGVK)
	eventually(t, 5*time.Second, func() bool {
		return k8sClient.Get(ctx, dbKey, redis) == nil
	})

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(cnpgClusterGVK)
	if err := k8sClient.Get(ctx, dbKey, cluster); err == nil {
		t.Error("expected no CNPG cluster for a light-tier shop, but one was found")
	}
}

func TestShopCRD_rejectsInvalidAvailability(t *testing.T) {
	ctx := context.Background()
	shop := &shopv1.Shop{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-it-bad-availability", Namespace: "default"},
		Spec: shopv1.ShopSpec{
			Name:          "Bad Shop",
			Availability:  "extreme",
			WalletAddress: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
			DatabaseKind:  shopv1.ShopDatabaseKindStandard,
		},
	}
	if err := k8sClient.Create(ctx, shop); err == nil {
		t.Fatal("expected the API server to reject an invalid availability value, got no error")
	}
}

func TestShopCRD_rejectsMissingWalletAddress(t *testing.T) {
	ctx := context.Background()
	shop := &shopv1.Shop{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-it-missing-wallet", Namespace: "default"},
		Spec: shopv1.ShopSpec{
			Name:         "No Wallet Shop",
			Availability: shopv1.ShopAvailabilityStandard,
			DatabaseKind: shopv1.ShopDatabaseKindStandard,
		},
	}
	if err := k8sClient.Create(ctx, shop); err == nil {
		t.Fatal("expected the API server to reject a Shop missing spec.walletAddress, got no error")
	}
}
