package cli

import "github.com/xymorphic/morph/internal/config"

// ConfigUpdate describes config update.
type ConfigUpdate = config.ConfigUpdate

// ConfigValue describes config value.
type ConfigValue = config.ConfigValue

// GetConfigValues returns config values.
func GetConfigValues(envPath string, configPath string, paths []string) ([]ConfigValue, error) {
	return config.GetConfigValues(envPath, configPath, paths)
}

// SetConfigValue updates config value.
func SetConfigValue(envPath string, configPath string, path string, value string) (string, error) {
	return config.SetConfigValue(envPath, configPath, path, value)
}

// SetConfigValues updates config values.
func SetConfigValues(envPath string, configPath string, updates []ConfigUpdate) ([]string, error) {
	return config.SetConfigValues(envPath, configPath, updates)
}

// NormalizeConfigPathAlias normalizes config path alias.
func NormalizeConfigPathAlias(path string) string {
	return config.NormalizeConfigPathAlias(path)
}
