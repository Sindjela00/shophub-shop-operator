package v1

// ShopSpec defines the desired state of Shop.
// TODO: name, availability (standard=2 replicas | high=3 replicas), wallet address, database kind (standard=PostgreSQL | light=Redis).
type ShopSpec struct{}

// ShopStatus defines the observed state of Shop.
type ShopStatus struct{}

// Shop is the Schema for the shops API.
type Shop struct {
	Spec   ShopSpec
	Status ShopStatus
}
