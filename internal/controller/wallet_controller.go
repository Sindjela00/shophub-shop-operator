package controller

import (
	"context"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
	"github.com/shophub/shophub-shop-operator/internal/ethaddr"
)

// WalletReconciler reconciles a Wallet object.
//
// The payout address itself is supplied by the shop owner (via the Shop's spec.walletAddress,
// copied onto the Wallet at creation time) rather than generated here — the operator has no
// private key material to mint a new on-chain account from. "Provisioning" the wallet means
// validating that address is well-formed (syntax + EIP-55 checksum) and recording that in
// status, so a typo'd payout address is surfaced immediately instead of silently failing the
// first time a customer tries to pay.
type WalletReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=apps.shophub.io,resources=wallets,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps.shophub.io,resources=wallets/status,verbs=get;update;patch

func (r *WalletReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var wallet shopv1.Wallet
	if err := r.Get(ctx, req.NamespacedName, &wallet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cond := metav1.Condition{Type: "Ready", ObservedGeneration: wallet.Generation}
	if err := ethaddr.Validate(wallet.Spec.Address); err != nil {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "InvalidAddress"
		cond.Message = err.Error()
	} else {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "AddressValid"
		cond.Message = "Payout address is a well-formed Ethereum address."
	}

	if apimeta.SetStatusCondition(&wallet.Status.Conditions, cond) {
		if err := r.Status().Update(ctx, &wallet); err != nil {
			return ctrl.Result{}, err
		}
	}

	logger.V(1).Info("reconciled wallet", "address", wallet.Spec.Address, "valid", cond.Status == metav1.ConditionTrue)
	return ctrl.Result{}, nil
}

func (r *WalletReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1.Wallet{}).
		Complete(r)
}
