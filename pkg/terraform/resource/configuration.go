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

package resource

func NopExternalNameInject(_ map[string]interface{}, _ string) {}

// NOTE(muvaf): Unfortunately, there is no way to get the package path and the
// name of an anonymous function in runtime. So, we have to get a full path.

type ExternalName struct {
	// InjectFuncPath is the path to the inject function.
	// Example: github.com/crossplane/provider-aws/apis/rds/v1alpha1.InjectExternalName
	// By default, a no-op function is used.
	InjectFuncPath string

	// OmittedFields are the ones you'd like to be removed from the schema since
	// they are specified via external name. You can omit only the top level fields.
	// No field is omitted by default.
	OmittedFields map[string]struct{}
}

type ConfigurationOption func(*Configuration)

func WithExternalName(e ExternalName) ConfigurationOption {
	return func(c *Configuration) {
		c.ExternalName = e
	}
}

func NewConfiguration(version, kind, terraformResourceType string, opts ...ConfigurationOption) *Configuration {
	c := &Configuration{
		Version:               version,
		Kind:                  kind,
		TerraformResourceType: terraformResourceType,
		ExternalName: ExternalName{
			// TODO(muvaf): Not a good idea.
			InjectFuncPath: "github.com/crossplane-contrib/terrajet/pkg/terraform/resource.NopExternalNameInject",
			OmittedFields:  map[string]struct{}{},
		},
		TerraformIDFieldName: "id",
	}
	for _, f := range opts {
		f(c)
	}
	return c
}

type Configuration struct {
	Version               string
	Kind                  string
	ExternalName          ExternalName
	TerraformResourceType string
	TerraformIDFieldName  string
}
