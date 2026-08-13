package integration_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
)

func TestWalletReconciliation_marksReadyForAValidAddress(t *testing.T) {
	ctx := context.Background()
	wallet := &shopv1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "wallet-it-valid", Namespace: "default"},
		Spec:       shopv1.WalletSpec{ShopRef: "wallet-it-valid", Address: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
	}
	if err := k8sClient.Create(ctx, wallet); err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	eventually(t, 5*time.Second, func() bool {
		var got shopv1.Wallet
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wallet), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "Ready" {
				return c.Status == metav1.ConditionTrue
			}
		}
		return false
	})
}

func TestWalletReconciliation_marksNotReadyForAnInvalidAddress(t *testing.T) {
	ctx := context.Background()
	wallet := &shopv1.Wallet{
		ObjectMeta: metav1.ObjectMeta{Name: "wallet-it-invalid", Namespace: "default"},
		Spec:       shopv1.WalletSpec{ShopRef: "wallet-it-invalid", Address: "not-an-address"},
	}
	if err := k8sClient.Create(ctx, wallet); err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	eventually(t, 5*time.Second, func() bool {
		var got shopv1.Wallet
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wallet), &got); err != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "Ready" {
				return c.Status == metav1.ConditionFalse && c.Reason == "InvalidAddress"
			}
		}
		return false
	})
}
