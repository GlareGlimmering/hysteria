package trafficlogger

type Capabilities struct {
	AccountConsistency int `json:"accountConsistency"`
	AccountBandwidth   int `json:"accountBandwidth"`
}

func CurrentCapabilities() Capabilities {
	return Capabilities{AccountConsistency: 2, AccountBandwidth: 1}
}
