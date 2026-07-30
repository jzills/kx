package config

import "github.com/BurntSushi/toml"

// decodeTOML is isolated so the rest of the package doesn't depend on the TOML
// library's error shapes.
func decodeTOML(data []byte, target *map[string]any) error {
	_, err := toml.Decode(string(data), target)
	return err
}
