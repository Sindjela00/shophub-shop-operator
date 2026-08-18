package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DiscordChannelSpec defines the desired state of DiscordChannel.
type DiscordChannelSpec struct {
	// ShopRef is the name of the owning Shop resource, in the same namespace.
	// +kubebuilder:validation:MinLength=1
	ShopRef string `json:"shopRef"`

	// ChannelName is the desired Discord channel name for this shop's alert notifications.
	// +kubebuilder:validation:MinLength=1
	ChannelName string `json:"channelName"`

	// GuildID is the Discord server (guild) this channel should be created in — the shop
	// owner's own server, once they've invited the bot to it. Left empty, the reconciler falls
	// back to the operator's own default guild (DISCORD_GUILD_ID), which is also how
	// shophub-app's own platform-level alert channel is provisioned (it never sets this field).
	// +optional
	GuildID string `json:"guildId,omitempty"`
}

// DiscordChannelStatus defines the observed state of DiscordChannel.
type DiscordChannelStatus struct {
	// +optional
	ChannelID string `json:"channelId,omitempty"`

	// WebhookID is the Discord webhook backing this channel's alert notifications. It is not
	// sensitive on its own (unlike the webhook's token/URL, which lives in the Secret named by
	// WebhookSecretRef) and is kept here so the controller can delete the webhook on cleanup.
	// +optional
	WebhookID string `json:"webhookId,omitempty"`

	// WebhookSecretRef is the name, in the same namespace, of the Secret holding this channel's
	// webhook URL (key "webhookUrl") — e.g. for Alertmanager to reference. The URL itself is
	// never stored here since it embeds the webhook's secret token.
	// +optional
	WebhookSecretRef string `json:"webhookSecretRef,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Shop",type=string,JSONPath=`.spec.shopRef`
// +kubebuilder:printcolumn:name="Channel",type=string,JSONPath=`.spec.channelName`

// DiscordChannel is the Schema for the discordchannels API.
type DiscordChannel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DiscordChannelSpec   `json:"spec,omitempty"`
	Status DiscordChannelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DiscordChannelList contains a list of DiscordChannel.
type DiscordChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DiscordChannel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DiscordChannel{}, &DiscordChannelList{})
}

// Hand-written stand-ins for controller-gen-generated DeepCopy methods — see the note in
// shop_types.go.

func (in *DiscordChannel) DeepCopyInto(out *DiscordChannel) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

func (in *DiscordChannel) DeepCopy() *DiscordChannel {
	if in == nil {
		return nil
	}
	out := new(DiscordChannel)
	in.DeepCopyInto(out)
	return out
}

func (in *DiscordChannel) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *DiscordChannelStatus) DeepCopyInto(out *DiscordChannelStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

func (in *DiscordChannelList) DeepCopyInto(out *DiscordChannelList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]DiscordChannel, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *DiscordChannelList) DeepCopy() *DiscordChannelList {
	if in == nil {
		return nil
	}
	out := new(DiscordChannelList)
	in.DeepCopyInto(out)
	return out
}

func (in *DiscordChannelList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
