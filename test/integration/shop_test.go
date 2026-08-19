package integration_test

import (
	"context"
	"strings"
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
	// Unlike the fake-client unit test, envtest runs a real apiserver, which is what actually
	// allocates NodePort values (apiserver REST storage strategy, not a separate controller) —
	// this is the one place that can prove a real port got allocated, not just that the
	// operator asked for NodePort.
	if svc.Spec.Type != corev1.ServiceTypeNodePort {
		t.Errorf("service type = %v, want NodePort", svc.Spec.Type)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].NodePort == 0 {
		t.Errorf("service ports = %+v, want a single port with a real allocated NodePort", svc.Spec.Ports)
	}

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

// TestShopReconciliation_reflectsAnInvalidWallet exercises the real ShopReconciler and
// WalletReconciler together against a real apiserver: a Shop whose Wallet CR (named the same
// as the Shop, per the shophub-app provisioning convention — see
// config/samples/apps_v1_wallet.yaml) has an invalid payout address must never settle into
// Ready, and its Ready condition should say why. Note envtest has no Deployment/ReplicaSet
// controller, so deployment.status.readyReplicas never advances here either — this test can't
// distinguish "blocked by the wallet" from "blocked because nothing ever rolls out" via phase
// alone, so it asserts on the condition message instead, which is set unconditionally.
func TestShopReconciliation_reflectsAnInvalidWallet(t *testing.T) {
	ctx := context.Background()
	shop := &shopv1.Shop{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-it-bad-wallet", Namespace: "default"},
		Spec: shopv1.ShopSpec{
			Name:          "IT Bad Wallet Shop",
			Availability:  shopv1.ShopAvailabilityStandard,
			WalletAddress: "not-an-address",
			DatabaseKind:  shopv1.ShopDatabaseKindStandard,
		},
	}
	if err := k8sClient.Create(ctx, shop); err != nil {
		t.Fatalf("create shop: %v", err)
	}
	wallet := &shopv1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: shop.Name, Namespace: shop.Namespace},
		Spec:       shopv1.WalletSpec{ShopRef: shop.Name, Address: shop.Spec.WalletAddress},
	}
	if err := k8sClient.Create(ctx, wallet); err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	eventually(t, 10*time.Second, func() bool {
		var got shopv1.Shop
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(shop), &got); err != nil {
			return false
		}
		if got.Status.Phase == shopv1.ShopPhaseReady {
			t.Fatalf("shop settled into Ready with an invalid Wallet: %+v", got.Status)
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "Ready" {
				return c.Status == metav1.ConditionFalse && strings.Contains(c.Message, "Wallet")
			}
		}
		return false
	})
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
