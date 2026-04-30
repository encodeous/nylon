package state

// ConfigState is a struct so we can easily build test harnesses and reload config
type ConfigState struct {
	CentralCfg
	LocalCfg
}
