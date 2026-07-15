package v1

// WalletSpec defines the desired state of Wallet.
// TODO: blockchain network and account/address provisioning for shop payouts.
type WalletSpec struct{}

// WalletStatus defines the observed state of Wallet.
type WalletStatus struct{}

// Wallet is the Schema for the wallets API.
type Wallet struct {
	Spec   WalletSpec
	Status WalletStatus
}
