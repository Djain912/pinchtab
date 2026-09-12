// Package extract fills a flat JSON-schema against a captured accessibility
// snapshot using the same semantic matcher that backs /find. It is model-free:
// no network, no model download, no browser. Given a schema and a node list it
// returns typed data plus per-field ref/score/confidence so an agent can fall
// back to /find on a low-confidence field.
package extract

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pinchtab/pinchtab/internal/selector"
)

// Type is a supported JSON-schema property type.
type Type string

const (
	TypeString  Type = "string"
	TypeNumber  Type = "number"
	TypeInteger Type = "integer"
	TypeBoolean Type = "boolean"
	TypeArray   Type = "array"
)

// hintKind classifies how a resolved x-pinchtab-hint drives matching.
type hintKind int

const (
	hintNone  hintKind = iota
	hintQuery          // a natural-language query string for the matcher
	hintRef            // a direct ref to select verbatim
)

// Property is one resolved schema property, ready to match.
type Property struct {
	Name        string
	Type        Type
	Description string
	Required    bool
	Hint        string

	// hintKindResolved and hintValue are computed by ParseSchema so Resolve
	// never re-parses the hint (and so hint errors surface at parse time).
	hintKindResolved hintKind
	hintValue        string
}

// Schema is a parsed object schema with properties in a deterministic order.
type Schema struct {
	Properties []Property
}

// UnsupportedError names the schema path that carries an unsupported construct.
type UnsupportedError struct {
	Path   string
	Reason string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Reason)
}

// rawSchema mirrors the accepted JSON-schema subset for decoding.
type rawSchema struct {
	Type       string                 `json:"type"`
	Required   []string               `json:"required"`
	Properties map[string]rawProperty `json:"properties"`
}

type rawProperty struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Hint        string          `json:"x-pinchtab-hint"`
	Properties  json.RawMessage `json:"properties"`
}

// ParseSchema parses a flat object schema. Unsupported constructs return an
// *UnsupportedError naming the offending path (e.g.
// "properties.price.type: object is not supported"). Arrays parse successfully
// and are reported as unsupported at resolution time, per the follow-up task.
func ParseSchema(data []byte) (Schema, error) {
	var raw rawSchema
	if err := json.Unmarshal(data, &raw); err != nil {
		return Schema{}, fmt.Errorf("parse schema: %w", err)
	}
	if raw.Type != "" && raw.Type != "object" {
		return Schema{}, &UnsupportedError{Path: "type", Reason: raw.Type + " is not supported"}
	}
	if len(raw.Properties) == 0 {
		return Schema{}, &UnsupportedError{Path: "properties", Reason: "an object schema with properties is required"}
	}

	required := make(map[string]bool, len(raw.Required))
	for _, name := range raw.Required {
		required[name] = true
	}

	names := make([]string, 0, len(raw.Properties))
	for name := range raw.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	props := make([]Property, 0, len(names))
	for _, name := range names {
		rp := raw.Properties[name]
		prop, err := parseProperty(name, rp, required[name])
		if err != nil {
			return Schema{}, err
		}
		props = append(props, prop)
	}
	return Schema{Properties: props}, nil
}

func parseProperty(name string, rp rawProperty, required bool) (Property, error) {
	path := "properties." + name
	switch Type(rp.Type) {
	case TypeString, TypeNumber, TypeInteger, TypeBoolean, TypeArray:
		// supported (array resolves to unsupported, but parses)
	case "":
		return Property{}, &UnsupportedError{Path: path + ".type", Reason: "a property type is required"}
	default:
		return Property{}, &UnsupportedError{Path: path + ".type", Reason: rp.Type + " is not supported"}
	}
	if len(rp.Properties) > 0 {
		return Property{}, &UnsupportedError{Path: path + ".properties", Reason: "nested object properties are not supported"}
	}

	prop := Property{
		Name:        name,
		Type:        Type(rp.Type),
		Description: strings.TrimSpace(rp.Description),
		Required:    required,
		Hint:        strings.TrimSpace(rp.Hint),
	}
	if err := resolveHint(&prop, path); err != nil {
		return Property{}, err
	}
	return prop, nil
}

// resolveHint validates x-pinchtab-hint against the selector kinds a node list
// can answer and caches the matcher-ready form. css:/xpath: need a browser and
// return an *UnsupportedError naming the property path.
func resolveHint(prop *Property, path string) error {
	hint := prop.Hint
	if hint == "" {
		prop.hintKindResolved = hintNone
		return nil
	}

	hintPath := path + ".x-pinchtab-hint"
	if !selector.HasKnownPrefix(hint) {
		// A bare hint is a natural-language query verbatim.
		prop.hintKindResolved = hintQuery
		prop.hintValue = hint
		return nil
	}

	sel := selector.Parse(hint)
	switch sel.Kind {
	case selector.KindCSS, selector.KindXPath:
		return &UnsupportedError{Path: hintPath, Reason: string(sel.Kind) + " selectors need a browser and are not supported"}
	case selector.KindText:
		prop.hintKindResolved = hintQuery
		prop.hintValue = sel.Value
		return nil
	case selector.KindRef:
		prop.hintKindResolved = hintRef
		prop.hintValue = sel.Value
		return nil
	}

	query, ok := sel.SemanticQuery()
	if !ok {
		return &UnsupportedError{Path: hintPath, Reason: string(sel.Kind) + " hints are not supported"}
	}
	prop.hintKindResolved = hintQuery
	prop.hintValue = query
	return nil
}
