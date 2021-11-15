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
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crossplane-contrib/terrajet/pkg/config"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
)

// Run runs the Terrajet code generation pipelines.
func Run(pc *config.Provider, rootDir string) { // nolint:gocyclo
	// Note(turkenh): nolint reasoning - this is the main function of the code
	// generation pipeline. We didn't want to split it into multiple functions
	// for better readability considering the straightforward logic here.

	// Group resources based on their Group and API Versions.
	resourcesGroups := map[string]map[string]map[string]*config.Resource{}
	for name, resource := range pc.Resources {
		if len(resourcesGroups[resource.ShortGroup]) == 0 {
			resourcesGroups[resource.ShortGroup] = map[string]map[string]*config.Resource{}
		}
		if len(resourcesGroups[resource.ShortGroup][resource.Version]) == 0 {
			resourcesGroups[resource.ShortGroup][resource.Version] = map[string]*config.Resource{}
		}
		resourcesGroups[resource.ShortGroup][resource.Version][name] = resource
	}

	// Add ProviderConfig API package to the list of API version packages.
	apiVersionPkgList := make([]string, 0)
	for _, p := range pc.BasePackages.APIVersion {
		apiVersionPkgList = append(apiVersionPkgList, filepath.Join(pc.ModulePath, p))
	}
	// Add ProviderConfig controller package to the list of controller packages.
	controllerPkgList := make([]string, 0)
	for _, p := range pc.BasePackages.Controller {
		controllerPkgList = append(controllerPkgList, filepath.Join(pc.ModulePath, p))
	}
	count := 0
	for shortGroup, versions := range resourcesGroups {
		group := pc.GroupSuffix
		if shortGroup != "" {
			group = strings.ToLower(shortGroup) + "." + pc.GroupSuffix
		}
		for version, resources := range versions {
			versionGen := NewVersionGenerator(rootDir, pc.ModulePath, group, version)
			crdGen := NewCRDGenerator(versionGen.Package(), rootDir, pc.ShortName, group, version)
			tfGen := NewTerraformedGenerator(versionGen.Package(), rootDir, group, version)
			ctrlGen := NewControllerGenerator(rootDir, pc.ModulePath, group)

			keys := make([]string, len(resources))
			i := 0
			for k := range resources {
				keys[i] = k
				i++
			}
			sort.Strings(keys)

			for _, name := range keys {
				if err := crdGen.Generate(resources[name]); err != nil {
					panic(errors.Wrapf(err, "cannot generate crd for resource %s", name))
				}
				if err := tfGen.Generate(resources[name]); err != nil {
					panic(errors.Wrapf(err, "cannot generate terraformed for resource %s", name))
				}
				ctrlPkgPath, err := ctrlGen.Generate(resources[name], versionGen.Package().Path())
				if err != nil {
					panic(errors.Wrapf(err, "cannot generate controller for resource %s", name))
				}
				controllerPkgList = append(controllerPkgList, ctrlPkgPath)
				count++
			}

			if err := versionGen.Generate(); err != nil {
				panic(errors.Wrap(err, "cannot generate version files"))
			}
			apiVersionPkgList = append(apiVersionPkgList, versionGen.Package().Path())
		}
	}

	if err := NewRegisterGenerator(rootDir, pc.ModulePath).Generate(apiVersionPkgList); err != nil {
		panic(errors.Wrap(err, "cannot generate register file"))
	}
	if err := NewSetupGenerator(rootDir, pc.ModulePath).Generate(controllerPkgList); err != nil {
		panic(errors.Wrap(err, "cannot generate setup file"))
	}
	apisDir := filepath.Clean(filepath.Join(rootDir, "apis"))
	internalDir := filepath.Clean(filepath.Join(rootDir, "internal"))
	if out, err := exec.Command("bash", "-c", fmt.Sprintf("goimports -w $(find %s -iname 'zz_*')", apisDir)).CombinedOutput(); err != nil {
		panic(errors.Wrap(err, "cannot run goimports for apis folder: "+string(out)))
	}
	if out, err := exec.Command("bash", "-c", fmt.Sprintf("goimports -w $(find %s -iname 'zz_*')", internalDir)).CombinedOutput(); err != nil {
		panic(errors.Wrap(err, "cannot run goimports for internal folder: "+string(out)))
	}

	fmt.Printf("\nGenerated %d resources!\n", count)
}
