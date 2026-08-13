package controller

import (
	"context"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
	"github.com/shophub/shophub-shop-operator/internal/discord"
)

// discordChannelFinalizer ensures the real Discord channel is deleted before the CR that
// represents it is allowed to go away — unlike the Deployment/Service/database CRs the Shop
// controller owns, Discord channels aren't garbage-collected by Kubernetes, so cleanup has to
// happen explicitly on delete.
const discordChannelFinalizer = "apps.shophub.io/discordchannel-cleanup"

// DiscordChannelReconciler reconciles a DiscordChannel object.
type DiscordChannelReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Discord *discord.Client
}

// +kubebuilder:rbac:groups=apps.shophub.io,resources=discordchannels,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps.shophub.io,resources=discordchannels/status,verbs=get;update;patch

func (r *DiscordChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var dc shopv1.DiscordChannel
	if err := r.Get(ctx, req.NamespacedName, &dc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !dc.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &dc)
	}

	if !controllerutil.ContainsFinalizer(&dc, discordChannelFinalizer) {
		controllerutil.AddFinalizer(&dc, discordChannelFinalizer)
		if err := r.Update(ctx, &dc); err != nil {
			return ctrl.Result{}, err
		}
	}

	name := discord.SanitizeChannelName(dc.Spec.ChannelName)

	// Idempotent: if we already recorded a channel ID, confirm it's still there rather than
	// blindly assuming a new one is needed (e.g. after a manager restart).
	if dc.Status.ChannelID != "" {
		existing, err := r.Discord.GetChannel(ctx, dc.Status.ChannelID)
		if err != nil {
			return r.markFailed(ctx, &dc, err)
		}
		if existing != nil {
			return ctrl.Result{}, nil
		}
		// Recorded channel is gone (deleted out-of-band) — fall through and recreate it.
	}

	// Guard against duplicate creation if a previous reconcile created the channel but failed
	// before persisting status.channelId.
	found, err := r.Discord.FindChannelByName(ctx, name)
	if err != nil {
		return r.markFailed(ctx, &dc, err)
	}

	channel := found
	if channel == nil {
		channel, err = r.Discord.CreateChannel(ctx, name)
		if err != nil {
			return r.markFailed(ctx, &dc, err)
		}
	}

	dc.Status.ChannelID = channel.ID
	apimeta.SetStatusCondition(&dc.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "ChannelProvisioned",
		Message: fmt.Sprintf("Discord channel #%s is provisioned.", name),
	})
	if err := r.Status().Update(ctx, &dc); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("provisioned discord channel", "channel", name, "channelId", channel.ID)
	return ctrl.Result{}, nil
}

func (r *DiscordChannelReconciler) reconcileDelete(ctx context.Context, dc *shopv1.DiscordChannel) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(dc, discordChannelFinalizer) {
		return ctrl.Result{}, nil
	}

	if dc.Status.ChannelID != "" {
		if err := r.Discord.DeleteChannel(ctx, dc.Status.ChannelID); err != nil {
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(dc, discordChannelFinalizer)
	if err := r.Update(ctx, dc); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DiscordChannelReconciler) markFailed(ctx context.Context, dc *shopv1.DiscordChannel, cause error) (ctrl.Result, error) {
	apimeta.SetStatusCondition(&dc.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "DiscordAPIError",
		Message: cause.Error(),
	})
	if err := r.Status().Update(ctx, dc); err != nil {
		return ctrl.Result{}, err
	}
	// Returning the error triggers controller-runtime's built-in exponential-backoff requeue.
	return ctrl.Result{}, cause
}

func (r *DiscordChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1.DiscordChannel{}).
		Complete(r)
}
