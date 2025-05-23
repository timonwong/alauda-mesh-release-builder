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
	"path"

	"github.com/alauda-mesh/release-builder/pkg/model"
	"github.com/alauda-mesh/release-builder/pkg/util"
)

// Docker builds all docker images and outputs them as tar.gz files
// docker.save in the repos does most of the work, we just need to call this and copy the files over
func Docker(manifest model.Manifest) error {
	// Build both default and distroless variants
	env := []string{"DOCKER_BUILD_VARIANTS=debug distroless"}

	if manifest.ProxyOverride != "" {
		// Add the vars to tell Istio to use our own Envoy binary
		env = append(env, "ISTIO_ENVOY_BASE_URL="+manifest.ProxyOverride)
	}

	target := "docker.save"
	if manifest.DockerOutput == model.DockerOutputContext {
		target = "docker"
	}
	if err := util.RunMake(manifest, "istio", env, target); err != nil {
		return fmt.Errorf("failed to create %v docker archives: %v", "istio", err)
	}
	if util.FileExists(path.Join(manifest.RepoOutDir("istio"), "docker")) {
		// Some repos output docker files to the source repo
		if err := util.CopyFilesToDir(path.Join(manifest.RepoOutDir("istio"), "docker"), path.Join(manifest.OutDir(), "docker")); err != nil {
			return fmt.Errorf("failed to package docker images: %v", err)
		}
	}

	return nil
}
