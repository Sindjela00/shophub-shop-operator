package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// WalletSpec defines the desired state of Wallet.
type WalletSpec struct {
	// ShopRef is the name of the owning Shop resource, in the same namespace.
	// +kubebuilder:validation:MinLength=1
	ShopRef string `json:"shopRef"`

	// Address is the on-chain payout address for this wallet — set from the owning Shop's
	// spec.walletAddress at creation time.
	// +kubebuilder:validation:MinLength=1
	Address string `json:"address"`
}

// WalletStatus defines the observed state of Wallet.
// TODO: populated by the (not yet implemented) Wallet controller.
type WalletStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Shop",type=string,JSONPath=`.spec.shopRef`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.spec.address`

// Wallet is the Schema for the wallets API.
type Wallet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WalletSpec   `json:"spec,omitempty"`
	Status WalletStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WalletList contains a list of Wallet.
type WalletList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Wallet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Wallet{}, &WalletList{})
}

// Hand-written stand-ins for controller-gen-generated DeepCopy methods — see the note in
// shop_types.go.

func (in *Wallet) DeepCopyInto(out *Wallet) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

func (in *Wallet) DeepCopy() *Wallet {
	if in == nil {
		return nil
	}
	out := new(Wallet)
	in.DeepCopyInto(out)
	return out
}

func (in *Wallet) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *WalletStatus) DeepCopyInto(out *WalletStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

func (in *WalletList) DeepCopyInto(out *WalletList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Wallet, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *WalletList) DeepCopy() *WalletList {
	if in == nil {
		return nil
	}
	out := new(WalletList)
	in.DeepCopyInto(out)
	return out
}

func (in *WalletList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
