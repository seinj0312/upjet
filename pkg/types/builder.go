/*
Copyright 2021 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package types

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"

	"github.com/crossplane/crossplane-runtime/pkg/fieldpath"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	twtypes "github.com/muvaf/typewriter/pkg/types"
	"github.com/pkg/errors"

	"github.com/crossplane-contrib/terrajet/pkg/comments"
	"github.com/crossplane-contrib/terrajet/pkg/config"
)

// NewBuilder returns a new Builder.
func NewBuilder(pkg *types.Package) *Builder {
	return &Builder{
		Package:  pkg,
		comments: twtypes.Comments{},
	}
}

// Builder is used to generate Go type equivalence of given Terraform schema.
type Builder struct {
	Package *types.Package

	genTypes []*types.Named
	comments twtypes.Comments
}

// Build returns parameters and observation types built out of Terraform schema.
func (g *Builder) Build(name string, schema *schema.Resource, cfg *config.Resource) ([]*types.Named, twtypes.Comments, error) {
	_, _, err := g.buildResource(schema, cfg, nil, nil, name)
	if len(cfg.Sensitive.CustomFieldPaths) > 0 {
		return nil, nil, errors.Errorf("following sensitive custom field paths not supported: %s", cfg.Sensitive.CustomFieldPaths)
	}
	return g.genTypes, g.comments, errors.Wrapf(err, "cannot build the types")
}

func (g *Builder) buildResource(res *schema.Resource, cfg *config.Resource, tfPath []string, xpPath []string, names ...string) (*types.Named, *types.Named, error) { //nolint:gocyclo
	// NOTE(muvaf): There can be fields in the same CRD with same name but in
	// different types. Since we generate the type using the field name, there
	// can be collisions. In order to be able to generate unique names consistently,
	// we need to process all fields in the same order all the time.
	keys := sortedKeys(res.Schema)

	paramTypeName, err := g.generateTypeName("Parameters", names...)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "cannot generate parameters type name of %s", fieldPath(names))
	}
	paramName := types.NewTypeName(token.NoPos, g.Package, paramTypeName, nil)

	obsTypeName, err := g.generateTypeName("Observation", names...)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "cannot generate observation type name of %s", fieldPath(names))
	}
	obsName := types.NewTypeName(token.NoPos, g.Package, obsTypeName, nil)

	// Note(turkenh): We don't know how many number of fields would be a
	// parameter or an observation in advance, hence opted for not to
	// preallocate (//nolint:prealloc). But we know a rough upper bound,
	// which is, len(keys), should we still do a preallocation here? Leaving
	// as it is given performance is not big concern during code generation.
	var paramFields []*types.Var //nolint:prealloc
	var paramTags []string       //nolint:prealloc
	var obsFields []*types.Var   //nolint:prealloc
	var obsTags []string         //nolint:prealloc
	for _, snakeFieldName := range keys {
		sch := res.Schema[snakeFieldName]
		fieldName := NewNameFromSnake(snakeFieldName)
		comment, err := comments.New(sch.Description)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "cannot build comment for description: %s", sch.Description)
		}
		tfTag := fieldName.Snake
		jsonTag := fieldName.LowerCamelComputed
		if comment.TerrajetOptions.FieldTFTag != nil {
			tfTag = *comment.TerrajetOptions.FieldTFTag
		}
		if comment.TerrajetOptions.FieldJSONTag != nil {
			jsonTag = *comment.TerrajetOptions.FieldJSONTag
		}

		tfPaths := append(tfPath, fieldName.Snake)
		xpPaths := append(xpPath, fieldName.LowerCamel)

		fieldType, err := g.buildSchema(sch, cfg, tfPaths, xpPaths, append(names, fieldName.Camel))
		if err != nil {
			return nil, nil, errors.Wrapf(err, "cannot infer type from schema of field %s", fieldName.Snake)
		}

		tfFieldPath := fieldPath(tfPaths)
		xpFieldPath := fieldPath(xpPaths)
		if ref, ok := cfg.References[tfFieldPath]; ok {
			comment.Reference = ref
			sch.Optional = true
		}

		fieldNameCamel := fieldName.Camel
		if e, ix := containsAt(cfg.Sensitive.CustomFieldPaths, tfFieldPath); e || sch.Sensitive {
			if e {
				cfg.Sensitive.CustomFieldPaths = remove(cfg.Sensitive.CustomFieldPaths, ix)
			}

			if isObservation(sch) {
				cfg.Sensitive.AddFieldPath(tfFieldPath, xpFieldPath)
				// Drop an observation field from schema if it is sensitive.
				// Data will be stored in connection details secret
				continue
			}
			sfx := "SecretRef"
			cfg.Sensitive.AddFieldPath(tfFieldPath, xpFieldPath+sfx)
			// todo(turkenh): do we need to support other field types as sensitive?
			if fieldType.String() != "string" && fieldType.String() != "*string" {
				return nil, nil, fmt.Errorf("got type \"%s\" for field \"%s\", only types \"string\" and \"*string\" supported as sensitive", fieldType.String(), fieldNameCamel)
			}
			// Replace a parameter field with secretKeyRef if it is sensitive.
			// If it is an observation field, it will be dropped.
			// Data will be loaded from the referenced secret key.
			fieldNameCamel += sfx
			// todo(hasan): do we need the pointer type if optional?
			fieldType = typeSecretKeySelector

			jsonTag += sfx
			tfTag = "-"
		}
		field := types.NewField(token.NoPos, g.Package, fieldNameCamel, fieldType, false)

		// NOTE(muvaf): If a field is not optional but computed, then it's
		// definitely an observation field.
		// If it's optional but also computed, then it means the field has a server
		// side default but user can change it, so it needs to go to parameters.
		switch {
		case isObservation(sch):
			obsFields = append(obsFields, field)
			obsTags = append(obsTags, fmt.Sprintf(`json:"%s,omitempty" tf:"%s"`, jsonTag, tfTag))
		default:
			if sch.Optional {
				paramTags = append(paramTags, fmt.Sprintf(`json:"%s,omitempty" tf:"%s"`, jsonTag, tfTag))
			} else {
				paramTags = append(paramTags, fmt.Sprintf(`json:"%s" tf:"%s"`, jsonTag, tfTag))
			}
			req := !sch.Optional
			comment.Required = &req
			paramFields = append(paramFields, field)
		}
		if ref, ok := cfg.References[tfFieldPath]; ok {
			refFields, refTags := g.generateReferenceFields(paramName, field, ref)
			paramTags = append(paramTags, refTags...)
			paramFields = append(paramFields, refFields...)
		}

		g.comments.AddFieldComment(paramName, fieldNameCamel, comment.Build())
	}

	// NOTE(muvaf): Not every struct has both computed and configurable fields,
	// so some of the types we generate here are empty and unnecessary. However,
	// there are valid types with zero fields and we don't have the information
	// to differentiate between valid zero fields and unnecessary one. So we generate
	// two structs for every complex type.
	// See usage of wafv2EmptySchema() in aws_wafv2_web_acl here:
	// https://github.com/hashicorp/terraform-provider-aws/blob/main/aws/wafv2_helper.go#L13
	paramType := types.NewNamed(paramName, types.NewStruct(paramFields, paramTags), nil)
	g.Package.Scope().Insert(paramType.Obj())
	g.genTypes = append(g.genTypes, paramType)

	obsType := types.NewNamed(obsName, types.NewStruct(obsFields, obsTags), nil)
	g.Package.Scope().Insert(obsType.Obj())
	g.genTypes = append(g.genTypes, obsType)

	return paramType, obsType, nil
}

func (g *Builder) buildSchema(sch *schema.Schema, cfg *config.Resource, tfPath []string, xpPath []string, names []string) (types.Type, error) { // nolint:gocyclo
	switch sch.Type {
	case schema.TypeBool:
		if sch.Optional {
			return types.NewPointer(types.Universe.Lookup("bool").Type()), nil
		}
		return types.Universe.Lookup("bool").Type(), nil
	case schema.TypeFloat:
		if sch.Optional {
			return types.NewPointer(types.Universe.Lookup("float64").Type()), nil
		}
		return types.Universe.Lookup("float64").Type(), nil
	case schema.TypeInt:
		if sch.Optional {
			return types.NewPointer(types.Universe.Lookup("int64").Type()), nil
		}
		return types.Universe.Lookup("int64").Type(), nil
	case schema.TypeString:
		if sch.Optional {
			return types.NewPointer(types.Universe.Lookup("string").Type()), nil
		}
		return types.Universe.Lookup("string").Type(), nil
	case schema.TypeMap, schema.TypeList, schema.TypeSet:
		tfPath = append(tfPath, "*")
		xpPath = append(xpPath, "*")
		var elemType types.Type
		var err error
		switch et := sch.Elem.(type) {
		case schema.ValueType:
			switch et {
			case schema.TypeBool:
				elemType = types.Universe.Lookup("bool").Type()
			case schema.TypeFloat:
				elemType = types.Universe.Lookup("float64").Type()
			case schema.TypeInt:
				elemType = types.Universe.Lookup("int64").Type()
			case schema.TypeString:
				elemType = types.Universe.Lookup("string").Type()
			case schema.TypeMap, schema.TypeList, schema.TypeSet, schema.TypeInvalid:
				return nil, errors.Errorf("element type of %s is basic but not one of known basic types", fieldPath(names))
			}
		case *schema.Schema:
			elemType, err = g.buildSchema(et, cfg, tfPath, xpPath, names)
			if err != nil {
				return nil, errors.Wrapf(err, "cannot infer type from schema of element type of %s", fieldPath(names))
			}
		case *schema.Resource:
			// TODO(muvaf): We skip the other type once we choose one of param
			// or obs types. This might cause some fields to be completely omitted.
			paramType, obsType, err := g.buildResource(et, cfg, tfPath, xpPath, names...)
			if err != nil {
				return nil, errors.Wrapf(err, "cannot infer type from resource schema of element type of %s", fieldPath(names))
			}

			// NOTE(muvaf): If a field is not optional but computed, then it's
			// definitely an observation field.
			// If it's optional but also computed, then it means the field has a server
			// side default but user can change it, so it needs to go to parameters.
			switch {
			case isObservation(sch):
				if obsType == nil {
					return nil, errors.Errorf("element type of %s is computed but the underlying schema does not return observation type", fieldPath(names))
				}
				elemType = obsType
			default:
				if paramType == nil {
					return nil, errors.Errorf("element type of %s is configurable but the underlying schema does not return a parameter type", fieldPath(names))
				}
				elemType = paramType
			}
		default:
			return nil, errors.Errorf("element type of %s should be either schema.Resource or schema.Schema", fieldPath(names))
		}

		// NOTE(muvaf): Maps and slices are already pointers, so we don't need to
		// wrap them even if they are optional.
		if sch.Type == schema.TypeMap {
			return types.NewMap(types.Universe.Lookup("string").Type(), elemType), nil
		}
		return types.NewSlice(elemType), nil
	case schema.TypeInvalid:
		return nil, errors.Errorf("invalid schema type %s", sch.Type.String())
	default:
		return nil, errors.Errorf("unexpected schema type %s", sch.Type.String())
	}
}

// generateTypeName generates a unique name for the type if its original name
// is used by another one. It adds the former field names recursively until it
// finds a unique name.
func (g *Builder) generateTypeName(suffix string, names ...string) (string, error) {
	n := names[len(names)-1] + suffix
	for i := len(names) - 2; i >= 0; i-- {
		if g.Package.Scope().Lookup(n) == nil {
			return n, nil
		}
		n = names[i] + n
	}
	if g.Package.Scope().Lookup(n) == nil {
		return n, nil
	}
	return "", errors.Errorf("could not generate a unique name for %s", n)
}

func isObservation(s *schema.Schema) bool {
	return s.Computed && !s.Optional
}

func sortedKeys(m map[string]*schema.Schema) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, len(m))
	i := 0
	for k := range m {
		keys[i] = k
		i++
	}
	sort.Strings(keys)
	return keys
}

func fieldPath(parts []string) string {
	seg := make(fieldpath.Segments, len(parts))
	for i, p := range parts {
		seg[i] = fieldpath.Field(p)
	}
	return seg.String()
}

func containsAt(ss []string, s string) (bool, int) {
	for i, v := range ss {
		if s == v {
			return true, i
		}
	}
	return false, -1
}

func remove(ss []string, i int) []string {
	ss[i] = ss[len(ss)-1]
	return ss[:len(ss)-1]
}
