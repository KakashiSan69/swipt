package api

import (
	_ "embed"
	"encoding/json"
)

//go:embed package.json
var manifest []byte

var Level, OldestLevel = func() (int64, int64) {
	var fields struct {
		APILevel       int64 `json:"apiLevel"`
		OldestAPILevel int64 `json:"oldestAPILevel"`
	}
	if err := json.Unmarshal(manifest, &fields); err != nil {
		panic(err)
	}
	return fields.APILevel, fields.OldestAPILevel
}()
