package controller

import (
	"context"
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
)

const (
	// defaultShopImage is the storefront image ShopHub publishes to GHCR (see
	// shophub-shop's own CI). Overridable via SHOP_IMAGE for local testing/forks.
	defaultShopImage  = "ghcr.io/sindjela00/shophub-shop:latest"
	shopImageEnvVar   = "SHOP_IMAGE"
	shopContainerPort = 8080

	defaultRedisImage = "redis:7.2-alpine"
	redisImageEnvVar  = "SHOP_REDIS_IMAGE"
)

var (
	cnpgClusterGVK = schema.GroupVersionKind{Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster"}
	redisGVK       = schema.GroupVersionKind{Group: "redis.redis.opstreelabs.in", Version: "v1beta2", Kind: "Redis"}
)

// ShopReconciler reconciles a Shop object: deploys the storefront app (Deployment + Service,
// replica count driven by spec.availability) and provisions its database — a CloudNativePG
// Cluster for the "standard" tier, or a single-instance Redis for "light" — by creating the
// custom resource their respective operators watch. Those operators (CloudNativePG,
// OT-CONTAINER-KIT's redis-operator) must already be installed in the cluster; this controller
// only ever talks to them via their CRDs, same as it would talk to any other Kubernetes API.
type ShopReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=apps.shophub.io,resources=shops,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps.shophub.io,resources=shops/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=redis.redis.opstreelabs.in,resources=redis,verbs=get;list;watch;create;update;patch

func (r *ShopReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var shop shopv1.Shop
	if err := r.Get(ctx, req.NamespacedName, &shop); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	deployment, err := r.reconcileDeployment(ctx, &shop)
	if err != nil {
		return r.markFailed(ctx, &shop, fmt.Errorf("deployment: %w", err))
	}

	if err := r.reconcileService(ctx, &shop); err != nil {
		return r.markFailed(ctx, &shop, fmt.Errorf("service: %w", err))
	}

	if err := r.reconcileDatabase(ctx, &shop); err != nil {
		return r.markFailed(ctx, &shop, fmt.Errorf("database: %w", err))
	}

	wantReplicas := replicasFor(shop.Spec.Availability)
	ready := deployment.Status.ReadyReplicas == wantReplicas

	phase := shopv1.ShopPhasePending
	if ready {
		phase = shopv1.ShopPhaseReady
	}
	shop.Status.Phase = phase
	apimeta.SetStatusCondition(&shop.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  conditionStatusFor(ready),
		Reason:  "Reconciled",
		Message: fmt.Sprintf("%d/%d replicas ready", deployment.Status.ReadyReplicas, wantReplicas),
	})
	if err := r.Status().Update(ctx, &shop); err != nil {
		return ctrl.Result{}, err
	}

	logger.V(1).Info("reconciled shop", "phase", phase)
	return ctrl.Result{}, nil
}

func (r *ShopReconciler) reconcileDeployment(ctx context.Context, shop *shopv1.Shop) (*appsv1.Deployment, error) {
	replicas := replicasFor(shop.Spec.Availability)
	labels := labelsFor(shop.Name)

	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: shop.Name, Namespace: shop.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec.Replicas = &replicas
		deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deployment.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "shop",
						Image: shopImage(),
						Ports: []corev1.ContainerPort{{ContainerPort: shopContainerPort}},
						Env: []corev1.EnvVar{
							{Name: "SHOP_NAME", Value: shop.Spec.Name},
							{Name: "SHOP_WALLET_ADDRESS", Value: shop.Spec.WalletAddress},
						},
					},
				},
			},
		}
		return controllerutil.SetControllerReference(shop, deployment, r.Scheme)
	})
	if err != nil {
		return nil, err
	}
	return deployment, nil
}

func (r *ShopReconciler) reconcileService(ctx context.Context, shop *shopv1.Shop) error {
	labels := labelsFor(shop.Name)
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: shop.Name, Namespace: shop.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: 80, TargetPort: intstr.FromInt32(shopContainerPort)},
		}
		return controllerutil.SetControllerReference(shop, svc, r.Scheme)
	})
	return err
}

func (r *ShopReconciler) reconcileDatabase(ctx context.Context, shop *shopv1.Shop) error {
	if shop.Spec.DatabaseKind == shopv1.ShopDatabaseKindLight {
		return r.reconcileRedis(ctx, shop)
	}
	return r.reconcileCNPGCluster(ctx, shop)
}

// reconcileCNPGCluster creates a minimal single-instance CloudNativePG Cluster. CNPG defaults
// bootstrap to a fresh `initdb` with an "app" database and a generated `<name>-app` Secret
// holding its credentials — sufficient for a shop's own storefront database.
func (r *ShopReconciler) reconcileCNPGCluster(ctx context.Context, shop *shopv1.Shop) error {
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(cnpgClusterGVK)
	cluster.SetName(dbName(shop.Name))
	cluster.SetNamespace(shop.Namespace)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cluster, func() error {
		if err := unstructured.SetNestedField(cluster.Object, int64(1), "spec", "instances"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(cluster.Object, "1Gi", "spec", "storage", "size"); err != nil {
			return err
		}
		return controllerutil.SetControllerReference(shop, cluster, r.Scheme)
	})
	return err
}

// reconcileRedis creates a minimal single-instance Redis (OT-CONTAINER-KIT's redis-operator),
// the "light" tier's database.
func (r *ShopReconciler) reconcileRedis(ctx context.Context, shop *shopv1.Shop) error {
	redis := &unstructured.Unstructured{}
	redis.SetGroupVersionKind(redisGVK)
	redis.SetName(dbName(shop.Name))
	redis.SetNamespace(shop.Namespace)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, redis, func() error {
		if err := unstructured.SetNestedField(redis.Object, redisImage(), "spec", "kubernetesConfig", "image"); err != nil {
			return err
		}
		return controllerutil.SetControllerReference(shop, redis, r.Scheme)
	})
	return err
}

func (r *ShopReconciler) markFailed(ctx context.Context, shop *shopv1.Shop, cause error) (ctrl.Result, error) {
	shop.Status.Phase = shopv1.ShopPhaseFailed
	apimeta.SetStatusCondition(&shop.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "ReconcileError",
		Message: cause.Error(),
	})
	if err := r.Status().Update(ctx, shop); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, cause
}

func (r *ShopReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1.Shop{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

func replicasFor(availability shopv1.ShopAvailability) int32 {
	if availability == shopv1.ShopAvailabilityHigh {
		return 3
	}
	return 2
}

func conditionStatusFor(ready bool) metav1.ConditionStatus {
	if ready {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func labelsFor(shopName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     "shophub-shop",
		"app.kubernetes.io/instance": shopName,
	}
}

func dbName(shopName string) string {
	return shopName + "-db"
}

func shopImage() string {
	if img := os.Getenv(shopImageEnvVar); img != "" {
		return img
	}
	return defaultShopImage
}

func redisImage() string {
	if img := os.Getenv(redisImageEnvVar); img != "" {
		return img
	}
	return defaultRedisImage
}
