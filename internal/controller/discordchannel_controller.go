package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

func webhookSecretName(dc *shopv1.DiscordChannel) string {
	return dc.Name + "-discord-webhook"
}

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
	var channel *discord.Channel
	if dc.Status.ChannelID != "" {
		existing, err := r.Discord.GetChannel(ctx, dc.Status.ChannelID)
		if err != nil {
			return r.markFailed(ctx, &dc, err)
		}
		channel = existing
		// If nil, the recorded channel is gone (deleted out-of-band) — fall through and
		// recreate it below.
	}

	if channel == nil {
		// Guard against duplicate creation if a previous reconcile created the channel but
		// failed before persisting status.channelId.
		found, err := r.Discord.FindChannelByName(ctx, name)
		if err != nil {
			return r.markFailed(ctx, &dc, err)
		}
		channel = found
		if channel == nil {
			channel, err = r.Discord.CreateChannel(ctx, name)
			if err != nil {
				return r.markFailed(ctx, &dc, err)
			}
		}
	}

	// Same idempotency pattern for the webhook Alertmanager will post through: look it up by
	// name before creating, so repeated reconciles never pile up duplicate webhooks.
	webhook, err := r.Discord.FindChannelWebhookByName(ctx, channel.ID, name)
	if err != nil {
		return r.markFailed(ctx, &dc, err)
	}
	if webhook == nil {
		webhook, err = r.Discord.CreateChannelWebhook(ctx, channel.ID, name)
		if err != nil {
			return r.markFailed(ctx, &dc, err)
		}
	}

	// The webhook URL embeds a secret token, so it goes in a Secret rather than the CR's
	// status (which isn't access-controlled the same way). Owning it via the DiscordChannel CR
	// means it's garbage-collected automatically when the CR goes away.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: webhookSecretName(&dc), Namespace: dc.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data["webhookUrl"] = []byte(webhook.URL())
		return controllerutil.SetControllerReference(&dc, secret, r.Scheme)
	}); err != nil {
		return r.markFailed(ctx, &dc, err)
	}

	dc.Status.ChannelID = channel.ID
	dc.Status.WebhookID = webhook.ID
	dc.Status.WebhookSecretRef = secret.Name
	apimeta.SetStatusCondition(&dc.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "ChannelProvisioned",
		Message: fmt.Sprintf("Discord channel #%s and its alert webhook are provisioned.", name),
	})
	if err := r.Status().Update(ctx, &dc); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("provisioned discord channel and webhook", "channel", name, "channelId", channel.ID, "webhookId", webhook.ID)
	return ctrl.Result{}, nil
}

func (r *DiscordChannelReconciler) reconcileDelete(ctx context.Context, dc *shopv1.DiscordChannel) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(dc, discordChannelFinalizer) {
		return ctrl.Result{}, nil
	}

	if dc.Status.WebhookID != "" {
		if err := r.Discord.DeleteWebhook(ctx, dc.Status.WebhookID); err != nil {
			return ctrl.Result{}, err
		}
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
		Owns(&corev1.Secret{}).
		Complete(r)
}
