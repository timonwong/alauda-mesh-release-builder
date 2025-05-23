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

package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"istio.io/istio/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/alauda-mesh/release-builder/pkg"
	"github.com/alauda-mesh/release-builder/pkg/model"
	"github.com/alauda-mesh/release-builder/pkg/util"
)

func NewReleaseInfo(release string) ReleaseInfo {
	tmpDir, err := os.MkdirTemp("/tmp", "release-test")
	if err != nil {
		panic(err)
	}
	log.Infof("test temporary dir at %s", tmpDir)

	manifest, err := pkg.ReadManifest(filepath.Join(release, "manifest.yaml"))
	if err != nil {
		panic(err)
	}

	if err := util.VerboseCommand("tar", "xvf", filepath.Join(release,
		fmt.Sprintf("istio-%s-linux-amd64.tar.gz", manifest.Version)), "-C", tmpDir).Run(); err != nil {
		log.Warnf("failed to unpackage release archive")
	}
	return ReleaseInfo{
		tmpDir:   tmpDir,
		manifest: manifest,
		archive:  filepath.Join(tmpDir, "istio-"+manifest.Version),
		release:  release,
	}
}

type ValidationFunction func(ReleaseInfo) error

type ReleaseInfo struct {
	tmpDir   string
	manifest model.Manifest
	archive  string
	release  string
}

func CheckRelease(release string) ([]string, string, []error) {
	if release == "" {
		return nil, "", []error{fmt.Errorf("--release must be passed")}
	}
	r := NewReleaseInfo(release)
	checks := map[string]ValidationFunction{
		"IstioctlArchive":    TestIstioctlArchive,
		"IstioctlStandalone": TestIstioctlStandalone,
		"TestDocker":         TestDocker,
		"HelmVersionsIstio":  TestHelmVersionsIstio,
		"HelmChartVersions":  TestHelmChartVersions,
		"IstioctlProfiles":   TestIstioctlProfiles,
		"Manifest":           TestManifest,
		"Licenses":           TestLicenses,
		"Grafana":            TestGrafana,
		"CompletionFiles":    TestCompletionFiles,
		"ProxyVersion":       TestProxyVersion,
		"Debian":             TestDebian,
		"Rpm":                TestRpm,
	}
	var errors []error
	var success []string
	for name, check := range checks {
		err := check(r)
		if err != nil {
			errors = append(errors, fmt.Errorf("check %v failed: %v", name, err))
		} else {
			success = append(success, name)
		}
	}
	sb := strings.Builder{}
	if len(errors) > 0 {
		sb.WriteString(fmt.Sprintf("Checks failed. Release info: %+v", r))
		sb.WriteString("Files in release: \n")
		_ = filepath.Walk(r.release,
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				sb.WriteString(fmt.Sprintf("- %s\n", path))
				return nil
			})
		sb.WriteString("\nFiles in archive: \n")
		_ = filepath.Walk(r.archive,
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				sb.WriteString(fmt.Sprintf("- %s\n", path))
				return nil
			})
	}
	return success, sb.String(), errors
}

func TestIstioctlArchive(r ReleaseInfo) error {
	// Check istioctl from archive
	buf := &bytes.Buffer{}
	cmd := util.VerboseCommand(filepath.Join(r.archive, "bin", "istioctl"), "version", "--remote=false", "--short", "-ojson")
	cmd.Stdout = buf
	if err := cmd.Run(); err != nil {
		return err
	}
	var v Version
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		return fmt.Errorf("failed to unmarshal version information: %v", err)
	}

	if v.ClientVersion == nil {
		return fmt.Errorf("no client version found in version information")
	}

	if gotVersion := v.ClientVersion.Version; gotVersion != r.manifest.Version {
		return fmt.Errorf("expected proxy version to be %s, got %s", r.manifest.Version, gotVersion)
	}
	return nil
}

func TestIstioctlStandalone(r ReleaseInfo) error {
	// Check istioctl from stand-alone archive
	istioctlArchivePath := filepath.Join(r.release, fmt.Sprintf("istioctl-%s-linux-amd64.tar.gz", r.manifest.Version))
	if err := util.VerboseCommand("tar", "xvf", istioctlArchivePath, "-C", r.tmpDir).Run(); err != nil {
		return err
	}
	buf := &bytes.Buffer{}
	cmd := util.VerboseCommand(filepath.Join(r.tmpDir, "istioctl"), "version", "--remote=false", "--short", "-ojson")
	cmd.Stdout = buf
	if err := cmd.Run(); err != nil {
		return err
	}
	var v Version
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		return fmt.Errorf("failed to unmarshal version information: %v", err)
	}

	if v.ClientVersion == nil {
		return fmt.Errorf("no client version found in version information")
	}

	if gotVersion := v.ClientVersion.Version; gotVersion != r.manifest.Version {
		return fmt.Errorf("expected proxy version to be %s, got %s", r.manifest.Version, gotVersion)
	}
	return nil
}

type GenericMap struct {
	data map[string]interface{}
}

func (g GenericMap) Path(path []string) (interface{}, error) {
	current := g.data
	var tmpList []interface{}
	for _, p := range path {
		val := current[p]
		// If the last path was a list, instead treat p as the index into that list
		if tmpList != nil {
			i, err := strconv.Atoi(p)
			if err != nil {
				return nil, fmt.Errorf("list requires integer path: %v in %v", p, path)
			}
			val = tmpList[i]
			tmpList = nil
		}
		switch v := val.(type) {
		case string:
			return v, nil
		case map[string]interface{}:
			current = v
		case []interface{}:
			tmpList = v
		default:
			return nil, fmt.Errorf("expected map or string, got %T for %v in %v", v, p, path)
		}
	}
	return nil, nil
}

func getValues(values []byte) (map[string]interface{}, error) {
	var typedValues map[string]interface{}
	if err := yaml.Unmarshal(values, &typedValues); err != nil {
		return nil, err
	}
	return typedValues, nil
}

func TestDocker(r ReleaseInfo) error {
	expected := []string{
		"pilot-distroless",
		"pilot-debug",
		"install-cni-debug",
		"ztunnel-debug",
		"ztunnel-distroless",
		"proxyv2-debug",
		"proxyv2-distroless",
	}
	found := map[string]struct{}{}
	d, err := os.ReadDir(filepath.Join(r.release, "docker"))
	if err != nil {
		return fmt.Errorf("failed to read docker dir: %v", err)
	}
	for _, i := range d {
		found[i.Name()] = struct{}{}
	}
	for _, plat := range r.manifest.Architectures {
		_, arch, _ := strings.Cut(plat, "/")
		suffix := ""
		if arch != "amd64" {
			suffix = "-" + arch
		}
		for _, i := range expected {
			image := i + suffix + ".tar.gz"
			if _, f := found[image]; !f {
				return fmt.Errorf("expected docker image %v, but had %v", image, found)
			}
		}
	}
	return nil
}

type DockerManifest struct {
	Config string `json:"Config"`
}

type DockerConfig struct {
	Config DockerConfigConfig `json:"config"`
}

type DockerConfigConfig struct {
	Env []string `json:"Env"`
}

// BuildInfo describes version information about the binary build.
type BuildInfo struct {
	Version       string `json:"version"`
	GitRevision   string `json:"revision"`
	GolangVersion string `json:"golang_version"`
	BuildStatus   string `json:"status"`
	GitTag        string `json:"tag"`
}

// Version holds info for client and control plane versions
type Version struct {
	ClientVersion *BuildInfo `json:"clientVersion,omitempty" yaml:"clientVersion,omitempty"`
}

func TestProxyVersion(r ReleaseInfo) error {
	archive := filepath.Join(r.release, "docker", "proxyv2-debug.tar.gz")
	if err := util.VerboseCommand("docker", "load", "-i", archive).Run(); err != nil {
		return fmt.Errorf("failed to load proxyv2-debug.tar.gz as docker image: %v", err)
	}
	buf := bytes.Buffer{}
	image := fmt.Sprintf("%s/%s:%s", r.manifest.Docker, "proxyv2", r.manifest.Version)
	cmd := util.VerboseCommand("docker", "run", "--rm", image, "version", "--short", "-ojson")
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return err
	}

	var v Version
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		return fmt.Errorf("failed to unmarshal version information: %v", err)
	}

	if v.ClientVersion == nil {
		return fmt.Errorf("no client version found in version information")
	}

	if gotVersion := v.ClientVersion.Version; gotVersion != r.manifest.Version {
		return fmt.Errorf("expected proxy version to be %s, got %s", r.manifest.Version, gotVersion)
	}
	return nil
}

func TestHelmChartVersions(r ReleaseInfo) error {
	if !util.IsValidSemver(r.manifest.Version) {
		log.Infof("Skipping TestHelmChartVersions; not a valid semver")
		return nil
	}
	expected := map[string]string{
		"cni":     "_internal_defaults_do_not_set.global",
		"ztunnel": "_internal_defaults_do_not_set",
		"istiod":  "_internal_defaults_do_not_set.global",
		"base":    "none",
		"gateway": "none",
	}
	for chart, path := range expected {
		buf := bytes.Buffer{}
		c := util.VerboseCommand("helm", "show", "values",
			filepath.Join(r.release, "helm", fmt.Sprintf("%s-%s.tgz", chart, r.manifest.Version)))
		c.Stdout = &buf
		if err := c.Run(); err != nil {
			return fmt.Errorf("helm show: %v", err)
		}
		if path == "none" {
			// Chart no hub/tag
			continue
		}
		if err := validateHubTag(r, buf.Bytes(), path); err != nil {
			return fmt.Errorf("%s: %v", chart, err)
		}
	}
	return nil
}

func TestHelmVersionsIstio(r ReleaseInfo) error {
	manifestValues := []string{
		"manifests/charts/gateways/istio-egress/values.yaml",
		"manifests/charts/gateways/istio-ingress/values.yaml",
		"manifests/charts/istio-cni/values.yaml",
		"manifests/charts/istio-control/istio-discovery/values.yaml",
	}
	topLevel := []string{"manifests/charts/ztunnel/values.yaml"}
	for _, file := range manifestValues {
		err := validateHubTagFromFile(r, file, "_internal_defaults_do_not_set.global")
		if err != nil {
			return err
		}
	}
	for _, file := range topLevel {
		err := validateHubTagFromFile(r, file, "_internal_defaults_do_not_set")
		if err != nil {
			return err
		}
	}
	return nil
}

func validateHubTagFromFile(r ReleaseInfo, file string, paths string) error {
	values, err := os.ReadFile(filepath.Join(r.archive, file))
	if err != nil {
		return err
	}
	return validateHubTag(r, values, paths)
}

func validateHubTag(r ReleaseInfo, valuesBytes []byte, paths string) error {
	values, err := getValues(valuesBytes)
	if err != nil {
		return err
	}
	tagPath := append(strings.Split(paths, "."), "tag")
	if paths == "" {
		tagPath = []string{"tag"}
	}
	tag, err := GenericMap{values}.Path(tagPath)
	if err != nil {
		return fmt.Errorf("invalid path: %v", err)
	}
	if tag != r.manifest.Version {
		return fmt.Errorf("archive tag incorrect: got %v expected %v", tag, r.manifest.Version)
	}
	hubPath := append(strings.Split(paths, "."), "hub")
	if paths == "" {
		hubPath = []string{"hub"}
	}
	hub, err := GenericMap{values}.Path(hubPath)
	if err != nil {
		return fmt.Errorf("invalid path: %v", err)
	}
	if hub != r.manifest.Docker {
		return fmt.Errorf("hub incorrect: got %v expected %v", hub, r.manifest.Docker)
	}
	return nil
}

func TestIstioctlProfiles(r ReleaseInfo) error {
	operatorChecks := []string{
		"manifests/profiles/default.yaml",
	}
	for _, f := range operatorChecks {
		by, err := os.ReadFile(filepath.Join(r.archive, f))
		if err != nil {
			return err
		}
		values, err := getValues(by)
		if err != nil {
			return err
		}
		tag, err := GenericMap{values}.Path([]string{"spec", "tag"})
		if err != nil {
			return fmt.Errorf("invalid path: %v", err)
		}
		if tag != r.manifest.Version {
			return fmt.Errorf("archive tag incorrect, got %v expected %v", tag, r.manifest.Version)
		}
		hub, err := GenericMap{values}.Path([]string{"spec", "hub"})
		if err != nil {
			return fmt.Errorf("invalid path: %v", err)
		}
		if hub != r.manifest.Docker {
			return fmt.Errorf("hub incorrect, got %v expected %v", hub, r.manifest.Docker)
		}
	}
	return nil
}

func TestManifest(r ReleaseInfo) error {
	for _, repo := range []string{"api", "client-go", "istio", "proxy"} {
		d, f := r.manifest.Dependencies.Get()[repo]
		if d == nil {
			return fmt.Errorf("missing dependency: %v", repo)
		}
		if !f || d.Sha == "" {
			return fmt.Errorf("got empty SHA for %v", repo)
		}
	}
	if r.manifest.Directory != "" {
		return fmt.Errorf("expected manifest directory to be hidden, got %v", r.manifest.Directory)
	}
	return nil
}

func TestGrafana(r ReleaseInfo) error {
	created := map[string]struct{}{}
	dir, err := os.ReadDir(path.Join(r.release, "grafana"))
	if err != nil {
		return err
	}
	for _, db := range dir {
		created[strings.TrimSuffix(db.Name(), ".json")] = struct{}{}
	}
	manifest := map[string]struct{}{}
	for dashboard := range r.manifest.GrafanaDashboards {
		manifest[dashboard] = struct{}{}
	}
	if !reflect.DeepEqual(created, manifest) {
		return fmt.Errorf("dashboards out of sync, release contains %+v, manifest contains %+v", created, manifest)
	}
	return nil
}

func TestLicenses(r ReleaseInfo) error {
	l, err := os.ReadDir(filepath.Join(r.release, "licenses"))
	if err != nil {
		return err
	}
	// Expect to find license folders for these repos
	expect := map[string]struct{}{
		"istio.tar.gz":           {},
		"client-go.tar.gz":       {},
		"tools.tar.gz":           {},
		"test-infra.tar.gz":      {},
		"release-builder.tar.gz": {},
	}

	for _, repo := range l {
		delete(expect, repo.Name())
	}

	if len(expect) > 0 {
		return fmt.Errorf("failed to find licenses for: %v", expect)
	}
	return nil
}

func TestCompletionFiles(r ReleaseInfo) error {
	for _, file := range []string{"istioctl.bash", "_istioctl"} {
		path := filepath.Join(r.archive, "tools", file)
		if !util.FileExists(path) {
			return fmt.Errorf("file not found %s", path)
		}
	}
	return nil
}

func TestDebian(info ReleaseInfo) error {
	if !fileExists(filepath.Join(info.release, "deb", "istio-sidecar.deb")) {
		return fmt.Errorf("debian package not found")
	}
	return nil
}

func TestRpm(info ReleaseInfo) error {
	if !fileExists(filepath.Join(info.release, "rpm", "istio-sidecar.rpm")) {
		return fmt.Errorf("rpm package not found")
	}
	return nil
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
