package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
	"github.com/shophub/shophub-shop-operator/internal/discord"
)

// alertmanagerConfigGVK is the Prometheus Operator's AlertmanagerConfig type. Referenced as
// unstructured rather than a typed import — this repo doesn't vendor the prometheus-operator
// Go module (same reasoning this controller set never vendored CNPG's types either), and the
// object this reconciler writes has a small, fixed shape that's easy to hand-build.
var alertmanagerConfigGVK = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"}

// discordChannelFinalizer ensures the real Discord channel is deleted before the CR that
// represents it is allowed to go away — unlike the Deployment/Service/database CRs the Shop
// controller owns, Discord channels aren't garbage-collected by Kubernetes, so cleanup has to
// happen explicitly on delete.
const discordChannelFinalizer = "apps.shophub.io/discordchannel-cleanup"

// DiscordChannelReconciler reconciles a DiscordChannel object.
type DiscordChannelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// DefaultGuildID is used for any DiscordChannel whose own Spec.GuildID is empty — the
	// operator's own configured guild (DISCORD_GUILD_ID), which is how shophub-app's own
	// platform alert channel and any not-yet-attached shop's channel are provisioned.
	DefaultGuildID string
	Discord        *discord.Client
}

// +kubebuilder:rbac:groups=apps.shophub.io,resources=discordchannels,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps.shophub.io,resources=discordchannels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=alertmanagerconfigs,verbs=get;list;watch;create;update;patch

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

	guildID := dc.Spec.GuildID
	if guildID == "" {
		guildID = r.DefaultGuildID
	}

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

	justCreated := false
	if channel == nil {
		// Guard against duplicate creation if a previous reconcile created the channel but
		// failed before persisting status.channelId.
		found, err := r.Discord.FindChannelByName(ctx, guildID, name)
		if err != nil {
			return r.markFailed(ctx, &dc, err)
		}
		channel = found
		if channel == nil {
			channel, err = r.Discord.CreateChannel(ctx, guildID, name)
			if err != nil {
				return r.markFailed(ctx, &dc, err)
			}
			justCreated = true
		}
	}

	// Best-effort only: a failed welcome message shouldn't fail the whole reconcile (and
	// requeue-retry it) when the channel/webhook — the parts that actually matter for
	// alerting — are otherwise fine. Gated on justCreated so it fires exactly once per channel:
	// a later reconcile that finds this same channel via FindChannelByName above never re-enters
	// this branch, so there's no need for a separate "already welcomed" status field.
	if justCreated {
		welcome := fmt.Sprintf("👋 This channel will receive alert notifications for **%s**.", name)
		if err := r.Discord.SendMessage(ctx, channel.ID, welcome); err != nil {
			logger.Error(err, "failed to send welcome message", "channel", name, "channelId", channel.ID)
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

	if err := r.reconcileAlertmanagerConfig(ctx, &dc, secret.Name); err != nil {
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

// reconcileAlertmanagerConfig creates or updates the AlertmanagerConfig that routes this shop's
// (or shophub-app's own) alerts to its Discord webhook. Previously this was a manually
// hand-copied object per shop (see shophub-kube-state's history) — generating it here means
// alerting actually reaches Discord with zero manual steps for every DiscordChannel that gets a
// working webhook. Matcher/receiver names follow the same "service=<this CR's name>" convention
// the manual version used, so it lines up with however the PrometheusRule alerts are labeled.
func (r *DiscordChannelReconciler) reconcileAlertmanagerConfig(ctx context.Context, dc *shopv1.DiscordChannel, webhookSecretName string) error {
	amConfig := &unstructured.Unstructured{}
	amConfig.SetGroupVersionKind(alertmanagerConfigGVK)
	amConfig.SetName(dc.Name + "-discord-routing")
	amConfig.SetNamespace(dc.Namespace)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, amConfig, func() error {
		receiverName := dc.Name + "-discord"
		amConfig.Object["spec"] = map[string]any{
			"route": map[string]any{
				"receiver": receiverName,
				"matchers": []any{
					map[string]any{"name": "service", "value": dc.Name, "matchType": "="},
				},
			},
			"receivers": []any{
				map[string]any{
					"name": receiverName,
					"discordConfigs": []any{
						map[string]any{
							"apiURL": map[string]any{"name": webhookSecretName, "key": "webhookUrl"},
						},
					},
				},
			},
		}
		return controllerutil.SetControllerReference(dc, amConfig, r.Scheme)
	})
	return err
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
