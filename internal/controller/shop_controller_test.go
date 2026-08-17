package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
)

const testWalletAddress = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"

func newShopTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := newTestScheme(t)
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client-go scheme: %v", err)
	}
	return scheme
}

func reconcileShop(t *testing.T, shop *shopv1.Shop) (client.Client, *shopv1.Shop) {
	t.Helper()
	scheme := newShopTestScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(shop).
		WithStatusSubresource(&shopv1.Shop{}).
		Build()

	r := &ShopReconciler{Client: fakeClient, Scheme: scheme}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: shop.Name, Namespace: shop.Namespace}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var got shopv1.Shop
	if err := fakeClient.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get shop returned error: %v", err)
	}
	return fakeClient, &got
}

func newShop(availability shopv1.ShopAvailability, dbKind shopv1.ShopDatabaseKind) *shopv1.Shop {
	return &shopv1.Shop{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-1", Namespace: "shops", UID: "test-uid"},
		Spec: shopv1.ShopSpec{
			Name:          "Aurora Shop",
			Availability:  availability,
			WalletAddress: testWalletAddress,
			DatabaseKind:  dbKind,
		},
	}
}

func TestShopReconciler_standardAvailabilityDeploysTwoReplicas(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityStandard, shopv1.ShopDatabaseKindStandard))

	var deployment appsv1.Deployment
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &deployment); err != nil {
		t.Fatalf("Get deployment returned error: %v", err)
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 2 {
		t.Errorf("replicas = %v, want 2", deployment.Spec.Replicas)
	}
}

func TestShopReconciler_highAvailabilityDeploysThreeReplicas(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityHigh, shopv1.ShopDatabaseKindStandard))

	var deployment appsv1.Deployment
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &deployment); err != nil {
		t.Fatalf("Get deployment returned error: %v", err)
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 3 {
		t.Errorf("replicas = %v, want 3", deployment.Spec.Replicas)
	}
}

func TestShopReconciler_createsServiceRoutingToTheDeployment(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityStandard, shopv1.ShopDatabaseKindStandard))

	var svc corev1.Service
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &svc); err != nil {
		t.Fatalf("Get service returned error: %v", err)
	}
	if svc.Spec.Selector["app.kubernetes.io/instance"] != "shop-1" {
		t.Errorf("service selector = %v, missing app.kubernetes.io/instance=shop-1", svc.Spec.Selector)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 80 {
		t.Errorf("service ports = %+v, want a single port 80", svc.Spec.Ports)
	}
}

func TestShopReconciler_standardDatabaseKindCreatesCNPGCluster(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityStandard, shopv1.ShopDatabaseKindStandard))

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(cnpgClusterGVK)
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1-db", Namespace: "shops"}, cluster); err != nil {
		t.Fatalf("Get CNPG cluster returned error: %v", err)
	}

	instances, found, err := unstructured.NestedInt64(cluster.Object, "spec", "instances")
	if err != nil || !found {
		t.Fatalf("spec.instances not found: found=%v err=%v", found, err)
	}
	if instances != 1 {
		t.Errorf("spec.instances = %d, want 1", instances)
	}
}

func TestShopReconciler_wiresReceivingWalletAddressForPayments(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityStandard, shopv1.ShopDatabaseKindStandard))

	var deployment appsv1.Deployment
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &deployment); err != nil {
		t.Fatalf("Get deployment returned error: %v", err)
	}

	var found *corev1.EnvVar
	for _, e := range deployment.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "Payments__ReceivingWalletAddress" {
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatal("Payments__ReceivingWalletAddress env var not set")
	}
	if found.Value != testWalletAddress {
		t.Errorf("Payments__ReceivingWalletAddress = %q, want %q", found.Value, testWalletAddress)
	}
}

func TestShopReconciler_standardDatabaseKindWiresConnectionStringFromCNPGSecret(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityStandard, shopv1.ShopDatabaseKindStandard))

	var deployment appsv1.Deployment
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &deployment); err != nil {
		t.Fatalf("Get deployment returned error: %v", err)
	}

	env := deployment.Spec.Template.Spec.Containers[0].Env
	byName := map[string]corev1.EnvVar{}
	for _, e := range env {
		byName[e.Name] = e
	}

	conn, ok := byName["ConnectionStrings__Default"]
	if !ok {
		t.Fatal("ConnectionStrings__Default env var not set")
	}
	wantValue := "Host=$(DB_HOST);Port=$(DB_PORT);Database=$(DB_NAME);Username=$(DB_USER);Password=$(DB_PASSWORD)"
	if conn.Value != wantValue {
		t.Errorf("ConnectionStrings__Default = %q, want %q", conn.Value, wantValue)
	}

	for _, want := range []struct{ name, key string }{
		{"DB_HOST", "host"}, {"DB_PORT", "port"}, {"DB_NAME", "dbname"},
		{"DB_USER", "username"}, {"DB_PASSWORD", "password"},
	} {
		e, ok := byName[want.name]
		if !ok || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Fatalf("%s not sourced from a secretKeyRef: %+v", want.name, e)
		}
		if e.ValueFrom.SecretKeyRef.Name != "shop-1-db-app" {
			t.Errorf("%s secret name = %q, want %q", want.name, e.ValueFrom.SecretKeyRef.Name, "shop-1-db-app")
		}
		if e.ValueFrom.SecretKeyRef.Key != want.key {
			t.Errorf("%s secret key = %q, want %q", want.name, e.ValueFrom.SecretKeyRef.Key, want.key)
		}
	}
}

func TestShopReconciler_lightDatabaseKindDoesNotSetConnectionString(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityStandard, shopv1.ShopDatabaseKindLight))

	var deployment appsv1.Deployment
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &deployment); err != nil {
		t.Fatalf("Get deployment returned error: %v", err)
	}

	for _, e := range deployment.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "ConnectionStrings__Default" {
			t.Errorf("ConnectionStrings__Default should not be set for the light/Redis tier, got %+v", e)
		}
	}
}

func TestShopReconciler_lightDatabaseKindCreatesRedisNotCNPG(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityStandard, shopv1.ShopDatabaseKindLight))

	redis := &unstructured.Unstructured{}
	redis.SetGroupVersionKind(redisGVK)
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1-db", Namespace: "shops"}, redis); err != nil {
		t.Fatalf("Get Redis returned error: %v", err)
	}

	image, found, err := unstructured.NestedString(redis.Object, "spec", "kubernetesConfig", "image")
	if err != nil || !found || image == "" {
		t.Errorf("spec.kubernetesConfig.image not set: found=%v err=%v image=%q", found, err, image)
	}

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(cnpgClusterGVK)
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1-db", Namespace: "shops"}, cluster); err == nil {
		t.Error("expected no CNPG cluster for a light-tier shop, but one was found")
	}
}

func TestShopReconciler_setsOwnerReferencesForGarbageCollection(t *testing.T) {
	fakeClient, _ := reconcileShop(t, newShop(shopv1.ShopAvailabilityStandard, shopv1.ShopDatabaseKindStandard))

	var deployment appsv1.Deployment
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &deployment); err != nil {
		t.Fatalf("Get deployment returned error: %v", err)
	}
	owners := deployment.GetOwnerReferences()
	if len(owners) != 1 || owners[0].Name != "shop-1" || owners[0].Kind != "Shop" {
		t.Errorf("deployment owner references = %+v, want a single Shop/shop-1 owner", owners)
	}
}
