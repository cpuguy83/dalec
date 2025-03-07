package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/invopop/jsonschema"
)

func main() {
	var r jsonschema.Reflector
	if err := r.AddGoComments("github.com/Azure/dalec", "./"); err != nil {
		panic(err)
	}

	schema := r.Reflect(&dalec.Spec{})
	if schema.PatternProperties == nil {
		schema.PatternProperties = make(map[string]*jsonschema.Schema)
	}
	schema.PatternProperties["^x-"] = &jsonschema.Schema{}

	dt, err := json.MarshalIndent(schema, "", "\t")
	if err != nil {
		panic(err)
	}

	if len(os.Args) > 1 {
		if err := os.MkdirAll(filepath.Dir(os.Args[1]), 0755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(os.Args[1], dt, 0644); err != nil {
			panic(err)
		}
		return
	}
	fmt.Println(string(dt))
}
