package subnet

// ConsumerKeyInfo holds assigned consumer key information
type ConsumerKeyInfo struct {
	ValidatorName    string `json:"validator_name"`
	ConsumerID       string `json:"consumer_id"`
	ConsumerPubKey   string `json:"consumer_pub_key"`
	ConsumerAddress  string `json:"consumer_address"`
	ProviderAddress  string `json:"provider_address"`
	AssignmentHeight int64  `json:"assignment_height"`
	PrivateKey       []byte `json:"-"` // Not serialized
}