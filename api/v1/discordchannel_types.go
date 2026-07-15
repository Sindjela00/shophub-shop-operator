package v1

// DiscordChannelSpec defines the desired state of DiscordChannel.
// TODO: Discord server/channel identifiers used for notifications and alerts.
type DiscordChannelSpec struct{}

// DiscordChannelStatus defines the observed state of DiscordChannel.
type DiscordChannelStatus struct{}

// DiscordChannel is the Schema for the discordchannels API.
type DiscordChannel struct {
	Spec   DiscordChannelSpec
	Status DiscordChannelStatus
}
