package api

import (
	_ "embed"
	"encoding/json"
)

//go:embed package.json
var manifest []byte

var Level = func() int64 {
	var fields struct {
		APILevel int64 `json:"apiLevel"`
	}
	if err := json.Unmarshal(manifest, &fields); err != nil {
		panic(err)
	}
	return fields.APILevel
}()
