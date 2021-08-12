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

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/iancoleman/strcase"
)

// NewBuilder returns a new Builder.
func NewBuilder(name string, source *schema.Resource, pkg *types.Package) *Builder {
	return &Builder{
		Name:     name,
		Source:   source,
		Package:  pkg,
		genTypes: map[string]*types.Named{},
	}
}

// Builder is used to generate Go type equivalence of given Terraform schema.
type Builder struct {
	Source  *schema.Resource
	Name    string
	Package *types.Package

	genTypes map[string]*types.Named
}

// Build returns parameters and observation types built out of Terraform schema.
func (g *Builder) Build() []*types.Named {
	_, _ = g.build(g.Name, g.Source)
	if len(g.genTypes) == 0 {
		return nil
	}
	result := make([]*types.Named, len(g.genTypes))
	i := 0
	for _, t := range g.genTypes {
		result[i] = t
		i++
	}
	return result
}

func (g *Builder) build(namePrefix string, s *schema.Resource) (*types.Named, *types.Named) { // nolint:gocyclo
	paramTypeName := strcase.ToCamel(namePrefix) + "Parameters"
	obsTypeName := strcase.ToCamel(namePrefix) + "Observation"
	if g.genTypes[paramTypeName] != nil && g.genTypes[obsTypeName] != nil {
		return g.genTypes[paramTypeName], g.genTypes[obsTypeName]
	}
	var paramFields []*types.Var
	var paramTags []string
	var obsFields []*types.Var
	var obsTags []string
	for n, sch := range s.Schema {
		fName := strcase.ToCamel(n)
		var field *types.Var
		switch sch.Type {
		case schema.TypeBool:
			field = types.NewField(token.NoPos, g.Package, fName, types.Universe.Lookup("bool").Type(), false)
		case schema.TypeFloat:
			field = types.NewField(token.NoPos, g.Package, fName, types.Universe.Lookup("float64").Type(), false)
		case schema.TypeInt:
			field = types.NewField(token.NoPos, g.Package, fName, types.Universe.Lookup("int64").Type(), false)
		case schema.TypeString:
			field = types.NewField(token.NoPos, g.Package, fName, types.Universe.Lookup("string").Type(), false)
		case schema.TypeList, schema.TypeSet:
			var elemType types.Type
			switch r := sch.Elem.(type) {
			case *schema.Resource:
				lParamType, lObsType := g.build(fName, r)
				switch {
				// There are fields that are computed only if user doesn't supply
				// input.
				case sch.Computed && sch.Optional:
					elemType = lParamType.Obj().Type()
				// If a field is not optional but computed, then it's definitely
				// an observation field.
				case sch.Computed:
					elemType = lObsType.Obj().Type()
				default:
					elemType = lParamType.Obj().Type()
				}
			case *schema.Schema:
				switch r.Type {
				case schema.TypeBool:
					elemType = types.Universe.Lookup("bool").Type()
				case schema.TypeFloat:
					elemType = types.Universe.Lookup("float64").Type()
				case schema.TypeInt:
					elemType = types.Universe.Lookup("int64").Type()
				case schema.TypeString:
					elemType = types.Universe.Lookup("string").Type()
				case schema.TypeMap, schema.TypeList, schema.TypeSet:
					panic("schema.Schema in list cannot have complex type")
				case schema.TypeInvalid:
					continue
				}
			}
			field = types.NewField(token.NoPos, g.Package, fName, types.NewSlice(elemType), false)
		case schema.TypeMap:
			var elemType types.Type
			switch r := sch.Elem.(type) {
			case *schema.Schema:
				switch r.Type {
				// According to documentation, maps cannot have non-simple element types.
				case schema.TypeBool:
					elemType = types.Universe.Lookup("bool").Type()
				case schema.TypeFloat:
					elemType = types.Universe.Lookup("float64").Type()
				case schema.TypeInt:
					elemType = types.Universe.Lookup("int64").Type()
				case schema.TypeString:
					elemType = types.Universe.Lookup("string").Type()
				case schema.TypeList, schema.TypeMap, schema.TypeSet:
					panic("value of map cannot be a complex type")
				case schema.TypeInvalid:
					continue
				}
			default:
				panic(fmt.Errorf("element of map has to have a schema"))
			}
			field = types.NewField(token.NoPos, g.Package, fName, types.NewMap(types.Universe.Lookup("string").Type(), elemType), false)
		case schema.TypeInvalid:
			continue
		}
		switch {
		// There are fields that are computed if user doesn't supply
		// input.
		case sch.Computed && sch.Optional:
			paramFields = append(paramFields, field)
			paramTags = append(paramTags, fmt.Sprintf("json:\"%s\" tf:\"%s\"", strcase.ToLowerCamel(n), n))
		// If a field is not optional but computed, then it's definitely
		// an observation field.
		case sch.Computed:
			obsFields = append(obsFields, field)
			obsTags = append(obsTags, fmt.Sprintf("json:\"%s\" tf:\"%s\"", strcase.ToLowerCamel(n), n))
		default:
			paramFields = append(paramFields, field)
			paramTags = append(paramTags, fmt.Sprintf("json:\"%s\" tf:\"%s\"", strcase.ToLowerCamel(n), n))
		}
	}
	var paramType, obsType *types.Named
	if len(paramFields) != 0 {
		tName := types.NewTypeName(token.NoPos, g.Package, paramTypeName, nil)
		paramType = types.NewNamed(tName, types.NewStruct(paramFields, paramTags), nil)
		g.genTypes[paramType.Obj().Name()] = paramType
	}
	if len(obsFields) != 0 {
		tName := types.NewTypeName(token.NoPos, g.Package, obsTypeName, nil)
		obsType = types.NewNamed(tName, types.NewStruct(obsFields, obsTags), nil)
		g.genTypes[obsType.Obj().Name()] = obsType
	}
	return paramType, obsType
}
