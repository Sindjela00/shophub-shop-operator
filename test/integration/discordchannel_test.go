package integration_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
)

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
