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

package pipeline

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/muvaf/typewriter/pkg/wrapper"
	"github.com/pkg/errors"

	"github.com/crossplane-contrib/terrajet/pkg/pipeline/templates"
)

// NewVersionGenerator returns a new VersionGenerator.
func NewVersionGenerator(rootDir, group, version string) *VersionGenerator {
	return &VersionGenerator{
		RootDir: rootDir,
		Group:   group,
		Version: version,
	}
}

// VersionGenerator generates files for a version of a specific group.
type VersionGenerator struct {
	RootDir string
	Group   string
	Version string
}

// Generate writes doc and group version info files to the disk.
func (vg *VersionGenerator) Generate() error {
	vars := map[string]interface{}{
		"CRD": map[string]string{
			"APIVersion": vg.Version,
			"Group":      vg.Group,
		},
	}
	pkgPath := filepath.Join(
		vg.RootDir,
		"apis",
		strings.ToLower(strings.Split(vg.Group, ".")[0]),
		strings.ToLower(vg.Version),
	)
	gviFile := wrapper.NewFile(pkgPath, vg.Version, templates.GroupVersionInfoTemplate,
		wrapper.WithGenStatement(GenStatement),
		wrapper.WithHeaderPath("hack/boilerplate.go.txt"), // todo
	)
	return errors.Wrap(
		gviFile.Write(filepath.Join(pkgPath, "zz_groupversion_info.go"), vars, os.ModePerm),
		"cannot write group version info file",
	)
}
