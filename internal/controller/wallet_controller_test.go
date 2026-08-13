package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
)

func TestWalletReconciler_marksReadyForAValidAddress(t *testing.T) {
	scheme := newTestScheme(t)
	wallet := &shopv1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-1", Namespace: "shops"},
		Spec:       shopv1.WalletSpec{ShopRef: "shop-1", Address: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(wallet).
		WithStatusSubresource(&shopv1.Wallet{}).
		Build()

	r := &WalletReconciler{Client: fakeClient, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "shop-1", Namespace: "shops"}}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var got shopv1.Wallet
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &got); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	cond := findCondition(got.Status.Conditions, "Ready")
	if cond == nil {
		t.Fatal("expected a Ready condition, found none")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("Ready condition status = %s, want True (reason=%s message=%s)", cond.Status, cond.Reason, cond.Message)
	}
}

func TestWalletReconciler_marksNotReadyForAnInvalidAddress(t *testing.T) {
	scheme := newTestScheme(t)
	wallet := &shopv1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-1", Namespace: "shops"},
		Spec:       shopv1.WalletSpec{ShopRef: "shop-1", Address: "not-an-address"},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(wallet).
		WithStatusSubresource(&shopv1.Wallet{}).
		Build()

	r := &WalletReconciler{Client: fakeClient, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "shop-1", Namespace: "shops"}}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var got shopv1.Wallet
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &got); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	cond := findCondition(got.Status.Conditions, "Ready")
	if cond == nil {
		t.Fatal("expected a Ready condition, found none")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready condition status = %s, want False", cond.Status)
	}
	if cond.Reason != "InvalidAddress" {
		t.Errorf("Ready condition reason = %s, want InvalidAddress", cond.Reason)
	}
}

func TestWalletReconciler_ignoresNotFound(t *testing.T) {
	scheme := newTestScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&shopv1.Wallet{}).Build()

	r := &WalletReconciler{Client: fakeClient, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "gone", Namespace: "shops"}}); err != nil {
		t.Errorf("Reconcile on a missing object returned error: %v, want nil", err)
	}
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
