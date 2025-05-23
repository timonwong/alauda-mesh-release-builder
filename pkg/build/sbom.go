// Copyright Istio Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package build

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"istio.io/istio/pkg/log"

	"github.com/alauda-mesh/release-builder/pkg/model"
	"github.com/alauda-mesh/release-builder/pkg/util"
)

// Sbom generates Software Bill Of Materials for istio repo in an SPDX readable format.
func GenerateBillOfMaterials(manifest model.Manifest) error {
	// Retrieve istio repository path to run the sbom generator
	istioRepoDir := manifest.RepoDir("istio")
	sourceSbomFile := path.Join(manifest.OutDir(), "istio-source.spdx")
	sourceSbomNamespace := fmt.Sprintf("https://storage.googleapis.com/istio-release/releases/%s/istio-source.spdx",
		manifest.Version)
	releaseSbomFile := path.Join(manifest.OutDir(), "istio-release.spdx")
	releaseSbomNamespace := fmt.Sprintf("https://storage.googleapis.com/istio-release/releases/%s/istio-release.spdx",
		manifest.Version)

	// construct all the docker image tarball names as bom currently cannot accept directory as input
	dockerDir := path.Join(manifest.OutDir(), "docker")
	dockerImages := []string{}
	if err := filepath.Walk(dockerDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi == nil {
			return fmt.Errorf("failed to get fileinfo for file at path %s", path)
		}
		if fi.IsDir() {
			return nil
		}
		dockerImages = append(dockerImages, path)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to walk directory %s: %v", dockerDir, err)
	}

	// Run bom generator to generate the software bill of materials(SBOM) for istio.
	log.Infof("Generating Software Bill of Materials for istio release artifacts")
	if err := util.VerboseCommand("bom", "--log-level", "error", "generate", "--name", "Istio Release "+manifest.Version,
		"--namespace", releaseSbomNamespace, "--ignore", "licenses,'*.sha256',docker", "--dirs", manifest.OutDir(),
		"--image-archive", strings.Join(dockerImages, ","), "--output", releaseSbomFile).Run(); err != nil {
		return fmt.Errorf("couldn't generate sbom for istio release artifacts: %v", err)
	}

	// Run bom generator to generate the software bill of materials(SBOM) for istio.
	log.Infof("Generating Software Bill of Materials for istio source code")
	if err := util.VerboseCommand("bom", "--log-level", "error", "generate", "--name", "Istio Source "+manifest.Version,
		"--namespace", sourceSbomNamespace, "--dirs", istioRepoDir, "--output", sourceSbomFile).Run(); err != nil {
		return fmt.Errorf("couldn't generate sbom for istio source: %v", err)
	}
	return nil
}
