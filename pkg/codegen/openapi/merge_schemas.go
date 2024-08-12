package openapi

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	"github.com/pb33f/libopenapi/orderedmap"
	"gopkg.in/yaml.v3"
)

// MergeSchemas merges all the fields in the schemas supplied into one giant schema.
// The idea is that we merge all fields together into one schema.
func MergeSchemas(allOf []*base.SchemaProxy, path []string) (Schema, error) {
	// If someone asked for the old way, for backward compatibility, return the
	// old style result.
	if globalState.options.Compatibility.OldMergeSchemas {
		return mergeSchemasV1(allOf, path)
	}
	return mergeSchemas(allOf, path)
}

func mergeSchemas(allOf []*base.SchemaProxy, path []string) (Schema, error) {
	n := len(allOf)

	if n == 1 {
		return GenerateGoSchema(allOf[0], path)
	}

	schema, err := valueWithPropagatedRef(allOf[0])
	if err != nil {
		return Schema{}, err
	}

	for i := 1; i < n; i++ {
		var err error
		oneOfSchema, err := valueWithPropagatedRef(allOf[i])
		if err != nil {
			return Schema{}, err
		}
		schema, err = mergeOpenapiSchemas(schema, oneOfSchema, true)
		if err != nil {
			return Schema{}, fmt.Errorf("error merging schemas for AllOf: %w", err)
		}
	}
	return GenerateGoSchema(base.CreateSchemaProxy(&schema), path)
}

// valueWithPropagatedRef returns a copy of ref schema with its Properties refs
// updated if ref itself is external. Otherwise, return ref.Value as-is.
func valueWithPropagatedRef(ref *base.SchemaProxy) (base.Schema, error) {
	schema := ref.Schema()
	if !ref.IsReference() || ref.GetReference()[0] == '#' {
		if schema == nil {
			return base.Schema{}, fmt.Errorf("TODO JVT")
		}
		return *schema, nil
	}

	pathParts := strings.Split(ref.GetReference(), "#")
	if len(pathParts) < 1 || len(pathParts) > 2 {
		return base.Schema{}, fmt.Errorf("unsupported reference: %s", ref.GetReference())
	}
	remoteComponent := pathParts[0]

	for _, value := range ToMap(schema.Properties) {
		if value.IsReference() && value.GetReference()[0] == '#' {
			// local reference, should propagate remote
			// value.Ref = remoteComponent + value.Ref // TODO confirm
			value.GoLow().SetReference(remoteComponent+value.GetReference(), nil)
		}
	}

	return *schema, nil
}

func mergeAllOf(allOf []*base.SchemaProxy) (base.Schema, error) {
	var schema base.Schema
	for _, schemaRef := range allOf {
		// var err error
		s2, err := schemaRef.BuildSchema()
		if err != nil {
			return base.Schema{}, fmt.Errorf("error merging schemas for AllOf: %w", err)
		}
		schema, err = mergeOpenapiSchemas(schema, *s2, true)
		if err != nil {
			return base.Schema{}, fmt.Errorf("error merging schemas for AllOf: %w", err)
		}
	}
	return schema, nil
}

// mergeOpenapiSchemas merges two openAPI schemas and returns the schema
// all of whose fields are composed.
func mergeOpenapiSchemas(s1, s2 base.Schema, allOf bool) (base.Schema, error) {
	var result base.Schema
	if s1.Extensions != nil || s2.Extensions != nil {
		result.Extensions = orderedmap.New[string, *yaml.Node]()
		if s1.Extensions != nil {
			for k, v := range ToMap(s1.Extensions) {
				result.Extensions.Set(k, v)
			}
		}
		if s2.Extensions != nil {
			for k, v := range ToMap(s2.Extensions) {
				result.Extensions.Set(k, v)
			}
		}
	}

	result.OneOf = append(s1.OneOf, s2.OneOf...)

	// We are going to make AllOf transitive, so that merging an AllOf that
	// contains AllOf's will result in a flat object.
	var err error
	if s1.AllOf != nil {
		var merged base.Schema
		merged, err = mergeAllOf(s1.AllOf)
		if err != nil {
			return base.Schema{}, fmt.Errorf("error transitive merging AllOf on schema 1")
		}
		s1 = merged
	}
	if s2.AllOf != nil {
		var merged base.Schema
		merged, err = mergeAllOf(s2.AllOf)
		if err != nil {
			return base.Schema{}, fmt.Errorf("error transitive merging AllOf on schema 2")
		}
		s2 = merged
	}

	result.AllOf = append(s1.AllOf, s2.AllOf...)

	s1Type := ""
	if len(s1.Type) > 0 {
		s1Type = s1.Type[0]
	}
	s2Type := ""
	if len(s2.Type) > 0 {
		s2Type = s2.Type[0]
	}

	if s1Type != "" && s2Type != "" && s1Type != s2Type {
		return base.Schema{}, errors.New("can not merge incompatible types")
	}
	result.Type = s1.Type

	if s1.Format != s2.Format {
		return base.Schema{}, errors.New("can not merge incompatible formats")
	}
	result.Format = s1.Format

	// For Enums, do we union, or intersect? This is a bit vague. I choose
	// to be more permissive and union.
	result.Enum = append(s1.Enum, s2.Enum...)

	// I don't know how to handle two different defaults.
	if s1.Default != nil || s2.Default != nil {
		return base.Schema{}, errors.New("merging two sets of defaults is undefined")
	}
	if s1.Default != nil {
		result.Default = s1.Default
	}
	if s2.Default != nil {
		result.Default = s2.Default
	}

	// We skip Example
	// We skip ExternalDocs

	// If two schemas disagree on any of these flags, we error out.
	if s1.UniqueItems != s2.UniqueItems {
		return base.Schema{}, errors.New("merging two schemas with different UniqueItems")

	}
	result.UniqueItems = s1.UniqueItems

	if s1.ExclusiveMinimum != nil {
		if s1.ExclusiveMinimum.IsB() {
			return base.Schema{}, errors.New("merging two schemas with left-hand-side ExclusiveMinimum defined as OpenAPI 3.1 type, not OpenAPI 3.0")
		}
		if s2.ExclusiveMinimum.IsB() {
			return base.Schema{}, errors.New("merging two schemas with right-hand-side ExclusiveMinimum defined as OpenAPI 3.1 type, not OpenAPI 3.0")
		}
		if s1.ExclusiveMinimum.A != s2.ExclusiveMinimum.A {
			return base.Schema{}, errors.New("merging two schemas with different ExclusiveMinimum")
		}
		result.ExclusiveMinimum = s1.ExclusiveMinimum
	}

	if s1.ExclusiveMaximum != nil {
		if s1.ExclusiveMaximum.IsB() {
			return base.Schema{}, errors.New("merging two schemas with left-hand-side ExclusiveMaximum defined as OpenAPI 3.1 type, not OpenAPI 3.0")
		}
		if s2.ExclusiveMaximum.IsB() {
			return base.Schema{}, errors.New("merging two schemas with right-hand-side ExclusiveMaximum defined as OpenAPI 3.1 type, not OpenAPI 3.0")
		}
		if s1.ExclusiveMaximum.A != s2.ExclusiveMaximum.A {
			return base.Schema{}, errors.New("merging two schemas with different ExclusiveMaximum")
		}
		result.ExclusiveMaximum = s1.ExclusiveMaximum
	}

	if s1.Nullable != s2.Nullable {
		return base.Schema{}, errors.New("merging two schemas with different Nullable")

	}
	result.Nullable = s1.Nullable

	if s1.ReadOnly != s2.ReadOnly {
		return base.Schema{}, errors.New("merging two schemas with different ReadOnly")

	}
	result.ReadOnly = s1.ReadOnly

	if s1.WriteOnly != s2.WriteOnly {
		return base.Schema{}, errors.New("merging two schemas with different WriteOnly")

	}
	result.WriteOnly = s1.WriteOnly

	// Required. We merge these.
	result.Required = append(s1.Required, s2.Required...)

	// We merge all properties
	result.Properties = orderedmap.New[string, *base.SchemaProxy]()
	for p := s1.Properties.First(); p != nil; p = p.Next() {
		result.Properties.Set(p.Key(), p.Value())
	}
	for p := s2.Properties.First(); p != nil; p = p.Next() {
		// TODO: detect conflicts
		result.Properties.Set(p.Key(), p.Value())
	}

	if isAdditionalPropertiesExplicitFalse(&s1) || isAdditionalPropertiesExplicitFalse(&s2) {
		result.AdditionalProperties = &base.DynamicValue[*base.SchemaProxy, bool]{N: 1, B: false}
	} else if s1.AdditionalProperties != nil && s1.AdditionalProperties.IsA() {
		if s2.AdditionalProperties != nil && s2.AdditionalProperties.IsA() {
			return base.Schema{}, errors.New("merging two schemas with additional properties, this is unhandled")
		} else {
			result.AdditionalProperties = s1.AdditionalProperties
		}
	} else {
		if s2.AdditionalProperties != nil {
			result.AdditionalProperties = s2.AdditionalProperties
		} else {
			if (s1.AdditionalProperties != nil && s1.AdditionalProperties.IsB() && s1.AdditionalProperties.B) || (s2.AdditionalProperties != nil && s2.AdditionalProperties.IsB() && s2.AdditionalProperties.B) {
				result.AdditionalProperties = &base.DynamicValue[*base.SchemaProxy, bool]{N: 1, B: true}
			}
		}
	}

	return result, nil
}
