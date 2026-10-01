package proxy

import (
	"encoding/json"
)

type RenderedConfig struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Routes        []Route `json:"routes"`
}

func renderBuiltin(set *RouteSet) ([]byte, error) {
	config := RenderedConfig{SchemaVersion: 1, Kind: "builtin", Routes: set.Routes}
	if config.Routes == nil {
		config.Routes = []Route{}
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
