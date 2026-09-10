// Command gen-contracts regenerates the JSON manifest schema from shared Go types.
package main

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattmezza/mrkt/internal/manifest"
	"os"
)

func main() {
	schema, err := jsonschema.For[manifest.Manifest](nil)
	if err != nil {
		panic(err)
	}
	schema.Schema = "https://json-schema.org/draft/2020-12/schema"
	schema.Title = "mrkt release manifest v1"
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		panic(err)
	}
	doc["required"] = []string{"version", "project"}
	props := doc["properties"].(map[string]any)
	props["version"].(map[string]any)["const"] = 1
	file := props["files"].(map[string]any)["items"].(map[string]any)
	file["required"] = []string{"path", "content_type"}
	fileProps := file["properties"].(map[string]any)
	fileProps["sha256"].(map[string]any)["pattern"] = "^([a-f0-9]{64})?$"
	fileProps["size"].(map[string]any)["minimum"] = 0
	fileProps["size"].(map[string]any)["maximum"] = manifest.MaxFileSize
	var enrich func(any)
	enrich = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if p, ok := x["properties"].(map[string]any); ok {
				if _, has := p["field"]; has {
					if _, has = p["op"]; has {
						x["required"] = []string{"field", "op"}
						p["op"].(map[string]any)["enum"] = []string{"eq", "ne", "gt", "gte", "lt", "lte", "exists"}
					}
				}
			}
			for _, child := range x {
				enrich(child)
			}
		case []any:
			for _, child := range x {
				enrich(child)
			}
		}
	}
	enrich(doc)
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.MkdirAll("schemas", 0755); err != nil {
		panic(err)
	}
	if err = os.WriteFile("schemas/manifest-v1.json", append(b, '\n'), 0644); err != nil {
		panic(err)
	}
}
