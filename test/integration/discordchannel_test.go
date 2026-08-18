package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
)

var alertmanagerConfigGVK = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"}

func TestDiscordChannelReconciliation_provisionsAChannel(t *testing.T) {
	ctx := context.Background()
	dc := &shopv1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "dc-it-1", Namespace: "default"},
		Spec:       shopv1.DiscordChannelSpec{ShopRef: "dc-it-1", ChannelName: "IT Test Channel"},
	}
	if err := k8sClient.Create(ctx, dc); err != nil {
		t.Fatalf("create discordchannel: %v", err)
	}

	eventually(t, 5*time.Second, func() bool {
		var got shopv1.DiscordChannel
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dc), &got); err != nil {
			return false
		}
		return got.Status.ChannelID != ""
	})
}

func TestDiscordChannelReconciliation_isIdempotentAcrossRepeatedReconciles(t *testing.T) {
	ctx := context.Background()
	dc := &shopv1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "dc-it-idempotent", Namespace: "default"},
		Spec:       shopv1.DiscordChannelSpec{ShopRef: "dc-it-idempotent", ChannelName: "Idempotency Check"},
	}
	if err := k8sClient.Create(ctx, dc); err != nil {
		t.Fatalf("create discordchannel: %v", err)
	}

	var channelID string
	eventually(t, 5*time.Second, func() bool {
		var got shopv1.DiscordChannel
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dc), &got); err != nil {
			return false
		}
		channelID = got.Status.ChannelID
		return channelID != ""
	})

	// The manager's informer resync alone would trigger further reconciles over this window;
	// give it several cycles' worth of time and confirm nothing changed.
	time.Sleep(1 * time.Second)

	var got shopv1.DiscordChannel
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dc), &got); err != nil {
		t.Fatalf("get discordchannel: %v", err)
	}
	if got.Status.ChannelID != channelID {
		t.Errorf("status.channelId changed from %q to %q across reconciles — the channel was recreated instead of reusing the existing one", channelID, got.Status.ChannelID)
	}
	if _, exists := discordTestData.channels[channelID]; !exists {
		t.Errorf("channel %q is missing from the fake Discord backend entirely", channelID)
	}
	// createCount (not total channel count) because the fake Discord backend is shared across
	// every test in this package — other tests create their own, differently-named channels.
	if count := discordTestData.createCount("idempotency-check"); count != 1 {
		t.Errorf("Discord create was called %d times for this channel, want exactly 1 — the reconciler created duplicates instead of confirming the existing one", count)
	}
}

func TestDiscordChannelReconciliation_provisionsAWebhookAndSecret(t *testing.T) {
	ctx := context.Background()
	dc := &shopv1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "dc-it-webhook", Namespace: "default"},
		Spec:       shopv1.DiscordChannelSpec{ShopRef: "dc-it-webhook", ChannelName: "Webhook Test Channel"},
	}
	if err := k8sClient.Create(ctx, dc); err != nil {
		t.Fatalf("create discordchannel: %v", err)
	}

	var got shopv1.DiscordChannel
	eventually(t, 5*time.Second, func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dc), &got); err != nil {
			return false
		}
		return got.Status.WebhookID != "" && got.Status.WebhookSecretRef != ""
	})

	var secret corev1.Secret
	secretKey := client.ObjectKey{Name: got.Status.WebhookSecretRef, Namespace: "default"}
	if err := k8sClient.Get(ctx, secretKey, &secret); err != nil {
		t.Fatalf("expected Secret %s to exist: %v", secretKey, err)
	}
	url, ok := secret.Data["webhookUrl"]
	if !ok || len(url) == 0 {
		t.Fatal("Secret did not contain a non-empty webhookUrl key")
	}
	if !strings.Contains(string(url), got.Status.WebhookID) {
		t.Errorf("webhookUrl %q does not reference webhook ID %q", url, got.Status.WebhookID)
	}

	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Name != dc.Name {
		t.Errorf("Secret owner references = %+v, want a single owner reference to %q", secret.OwnerReferences, dc.Name)
	}

	if count := discordTestData.webhookCreateCount("webhook-test-channel"); count != 1 {
		t.Errorf("Discord webhook create was called %d times, want exactly 1", count)
	}
}

func TestDiscordChannelReconciliation_deletesTheWebhookWhenTheCRIsDeleted(t *testing.T) {
	ctx := context.Background()
	dc := &shopv1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "dc-it-webhook-delete", Namespace: "default"},
		Spec:       shopv1.DiscordChannelSpec{ShopRef: "dc-it-webhook-delete", ChannelName: "Webhook Delete Channel"},
	}
	if err := k8sClient.Create(ctx, dc); err != nil {
		t.Fatalf("create discordchannel: %v", err)
	}

	var got shopv1.DiscordChannel
	eventually(t, 5*time.Second, func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dc), &got); err != nil {
			return false
		}
		return got.Status.WebhookID != ""
	})
	webhookID := got.Status.WebhookID

	if err := k8sClient.Delete(ctx, &got); err != nil {
		t.Fatalf("delete discordchannel: %v", err)
	}

	eventually(t, 5*time.Second, func() bool {
		var check shopv1.DiscordChannel
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dc), &check)
		return err != nil
	})

	if _, exists := discordTestData.webhooks[webhookID]; exists {
		t.Errorf("webhook %q still exists in the fake Discord backend after CR deletion", webhookID)
	}
}

func TestDiscordChannelReconciliation_generatesAlertmanagerConfig(t *testing.T) {
	ctx := context.Background()
	dc := &shopv1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "dc-it-amconfig", Namespace: "default"},
		Spec:       shopv1.DiscordChannelSpec{ShopRef: "dc-it-amconfig", ChannelName: "AM Config Channel"},
	}
	if err := k8sClient.Create(ctx, dc); err != nil {
		t.Fatalf("create discordchannel: %v", err)
	}

	var got shopv1.DiscordChannel
	eventually(t, 5*time.Second, func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dc), &got); err != nil {
			return false
		}
		return got.Status.WebhookSecretRef != ""
	})

	amConfig := &unstructured.Unstructured{}
	amConfig.SetGroupVersionKind(alertmanagerConfigGVK)
	key := client.ObjectKey{Name: dc.Name + "-discord-routing", Namespace: "default"}
	if err := k8sClient.Get(ctx, key, amConfig); err != nil {
		t.Fatalf("expected AlertmanagerConfig %s to exist: %v", key, err)
	}

	receivers, _, _ := unstructured.NestedSlice(amConfig.Object, "spec", "receivers")
	if len(receivers) != 1 {
		t.Fatalf("spec.receivers = %+v, want exactly one receiver", receivers)
	}
	receiverObj, _ := receivers[0].(map[string]any)
	discordConfigs, _ := receiverObj["discordConfigs"].([]any)
	if len(discordConfigs) != 1 {
		t.Fatalf("receivers[0].discordConfigs = %+v, want exactly one", discordConfigs)
	}
	apiURL, _ := discordConfigs[0].(map[string]any)["apiURL"].(map[string]any)
	if apiURL["name"] != got.Status.WebhookSecretRef {
		t.Errorf("spec.receivers[0].discordConfigs[0].apiURL.name = %v, want %q", apiURL["name"], got.Status.WebhookSecretRef)
	}

	if len(amConfig.GetOwnerReferences()) != 1 || amConfig.GetOwnerReferences()[0].Name != dc.Name {
		t.Errorf("AlertmanagerConfig owner references = %+v, want a single owner reference to %q", amConfig.GetOwnerReferences(), dc.Name)
	}
}

func TestDiscordChannelCRD_rejectsMissingChannelName(t *testing.T) {
	ctx := context.Background()
	dc := &shopv1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "dc-it-missing-name", Namespace: "default"},
		Spec:       shopv1.DiscordChannelSpec{ShopRef: "dc-it-missing-name"},
	}
	if err := k8sClient.Create(ctx, dc); err == nil {
		t.Fatal("expected the API server to reject a DiscordChannel missing spec.channelName, got no error")
	}
}
