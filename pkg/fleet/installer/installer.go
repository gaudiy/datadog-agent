// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package installer provides a package manager that installs and uninstalls packages.
package installer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/datadog-agent/pkg/fleet/internal/paths"

	"github.com/DataDog/datadog-agent/pkg/fleet/env"
	"github.com/DataDog/datadog-agent/pkg/fleet/installer/repository"
	"github.com/DataDog/datadog-agent/pkg/fleet/installer/service"
	"github.com/DataDog/datadog-agent/pkg/fleet/internal/db"
	"github.com/DataDog/datadog-agent/pkg/fleet/internal/oci"
	"github.com/DataDog/datadog-agent/pkg/util/filesystem"
	"github.com/DataDog/datadog-agent/pkg/util/log"
	"github.com/DataDog/datadog-agent/pkg/version"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	packageDatadogAgent     = "datadog-agent"
	packageAPMInjector      = "datadog-apm-inject"
	packageDatadogInstaller = "datadog-installer"
)

var (
	fsDisk = filesystem.NewDisk()
)

// Installer is a package manager that installs and uninstalls packages.
type Installer interface {
	IsInstalled(ctx context.Context, pkg string) (bool, error)
	State(pkg string) (repository.State, error)
	States() (map[string]repository.State, error)

	Install(ctx context.Context, url string, args []string) error
	Remove(ctx context.Context, pkg string) error
	Purge(ctx context.Context)

	InstallExperiment(ctx context.Context, url string) error
	RemoveExperiment(ctx context.Context, pkg string) error
	PromoteExperiment(ctx context.Context, pkg string) error

	GarbageCollect(ctx context.Context) error

	InstrumentAPMInjector(ctx context.Context, method string) error
	UninstrumentAPMInjector(ctx context.Context, method string) error
}

// installerImpl is the implementation of the package manager.
type installerImpl struct {
	m sync.Mutex

	db                *db.PackagesDB
	downloader        *oci.Downloader
	repositories      *repository.Repositories
	configsDir        string
	packagesDir       string
	tmpDirPath        string
	packageInstallers map[string]packageInstaller
}

// NewInstaller returns a new Package Manager.
func NewInstaller(env *env.Env) (Installer, error) {
	err := ensurePackageDirExists()
	if err != nil {
		return nil, fmt.Errorf("could not ensure packages directory exists: %w", err)
	}
	db, err := db.New(filepath.Join(paths.PackagesPath, "packages.db"), db.WithTimeout(10*time.Second))
	if err != nil {
		return nil, fmt.Errorf("could not create packages db: %w", err)
	}
	i := &installerImpl{
		db:           db,
		downloader:   oci.NewDownloader(env, http.DefaultClient),
		repositories: repository.NewRepositories(paths.PackagesPath, paths.LocksPack),
		configsDir:   paths.DefaultConfigsDir,
		tmpDirPath:   paths.TmpDirPath,
		packagesDir:  paths.PackagesPath,
	}
	i.packageInstallers = map[string]packageInstaller{
		packageDatadogAgent: &datadogAgentPackageInstaller{
			basePackageInstaller: &basePackageInstaller{
				installerImpl: i,
				pkgName:       packageDatadogAgent,
			},
		},
		packageDatadogInstaller: &datadogInstallerPackageInstaller{
			basePackageInstaller: &basePackageInstaller{
				installerImpl: i,
				pkgName:       packageDatadogInstaller,
			},
		},
		packageAPMInjector: &apmInjectorPackageInstaller{
			basePackageInstaller: &basePackageInstaller{
				installerImpl: i,
				pkgName:       packageAPMInjector,
			},
		},
	}
	return i, nil
}

func (i *installerImpl) getInstallerPackageFor(pkgName string) packageInstaller {
	if pakInstaller, ok := i.packageInstallers[pkgName]; ok {
		return pakInstaller
	}
	// Return a default package installer with the given name for cases where we don't have a specific package installer.
	pakInstaller := &basePackageInstaller{
		installerImpl: i,
		pkgName:       pkgName,
	}
	if i.packageInstallers != nil {
		i.packageInstallers[pkgName] = pakInstaller
	}
	return pakInstaller
}

// State returns the state of a package.
func (i *installerImpl) State(pkg string) (repository.State, error) {
	return i.repositories.GetPackageState(pkg)
}

// States returns the states of all packages.
func (i *installerImpl) States() (map[string]repository.State, error) {
	return i.repositories.GetState()
}

// IsInstalled checks if a package is installed.
func (i *installerImpl) IsInstalled(_ context.Context, pkg string) (bool, error) {
	// The install script passes the package name as either <package>-<version> or <package>=<version>
	// depending on the platform so we strip the version prefix by looking for the "real" package name
	hasMatch := false
	for _, p := range PackagesList {
		if strings.HasPrefix(pkg, p.Name) {
			if hasMatch {
				return false, fmt.Errorf("the package %v matches multiple known packages", pkg)
			}
			pkg = p.Name
			hasMatch = true
		}
	}
	hasPackage, err := i.db.HasPackage(pkg)
	if err != nil {
		return false, fmt.Errorf("could not list packages: %w", err)
	}
	return hasPackage, nil
}

// Install installs or updates a package.
func (i *installerImpl) Install(ctx context.Context, url string, args []string) error {
	i.m.Lock()
	defer i.m.Unlock()
	pkg, err := i.downloader.Download(ctx, url)
	if err != nil {
		return fmt.Errorf("could not download package: %w", err)
	}
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
		span.SetTag(ext.ResourceName, pkg.Name)
		span.SetTag("package_version", pkg.Version)
	}

	for _, dependency := range packageDependencies[pkg.Name] {
		installed, err := i.IsInstalled(ctx, dependency)
		if err != nil {
			return fmt.Errorf("could not check if required package %s is installed: %w", dependency, err)
		}
		if !installed {
			// TODO: we should resolve the dependency version & install it instead
			return fmt.Errorf("required package %s is not installed", dependency)
		}
	}

	dbPkg, err := i.db.GetPackage(pkg.Name)
	if err != nil && !errors.Is(err, db.ErrPackageNotFound) {
		return fmt.Errorf("could not get package: %w", err)
	}
	if dbPkg.Name == pkg.Name && dbPkg.Version == pkg.Version {
		log.Infof("package %s version %s is already installed", pkg.Name, pkg.Version)
		return nil
	}
	err = checkAvailableDiskSpace(pkg, i.packagesDir)
	if err != nil {
		return fmt.Errorf("not enough disk space: %w", err)
	}
	tmpDir, err := os.MkdirTemp(i.tmpDirPath, fmt.Sprintf("tmp-install-stable-%s-*", pkg.Name)) // * is replaced by a random string
	if err != nil {
		return fmt.Errorf("could not create temporary directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	configDir := filepath.Join(i.configsDir, pkg.Name)
	err = pkg.ExtractLayers(oci.DatadogPackageLayerMediaType, tmpDir)
	if err != nil {
		return fmt.Errorf("could not extract package layers: %w", err)
	}
	err = pkg.ExtractLayers(oci.DatadogPackageConfigLayerMediaType, configDir)
	if err != nil {
		return fmt.Errorf("could not extract package config layer: %w", err)
	}
	err = i.getInstallerPackageFor(pkg.Name).SetupPackage(ctx, pkg.Version, tmpDir, args)
	if err != nil {
		return fmt.Errorf("could not setup package: %w", err)
	}
	err = i.db.SetPackage(db.Package{
		Name:             pkg.Name,
		Version:          pkg.Version,
		InstallerVersion: version.AgentVersion,
	})
	if err != nil {
		return fmt.Errorf("could not store package installation in db: %w", err)
	}
	return nil
}

// InstallExperiment installs an experiment on top of an existing package.
func (i *installerImpl) InstallExperiment(ctx context.Context, url string) error {
	i.m.Lock()
	defer i.m.Unlock()
	pkg, err := i.downloader.Download(ctx, url)
	if err != nil {
		return fmt.Errorf("could not download package: %w", err)
	}
	err = checkAvailableDiskSpace(pkg, i.packagesDir)
	if err != nil {
		return fmt.Errorf("not enough disk space: %w", err)
	}
	tmpDir, err := os.MkdirTemp(i.tmpDirPath, fmt.Sprintf("tmp-install-experiment-%s-*", pkg.Name)) // * is replaced by a random string
	if err != nil {
		return fmt.Errorf("could not create temporary directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	configDir := filepath.Join(i.configsDir, pkg.Name)
	err = pkg.ExtractLayers(oci.DatadogPackageLayerMediaType, tmpDir)
	if err != nil {
		return fmt.Errorf("could not extract package layers: %w", err)
	}
	err = pkg.ExtractLayers(oci.DatadogPackageConfigLayerMediaType, configDir)
	if err != nil {
		return fmt.Errorf("could not extract package config layer: %w", err)
	}
	return i.getInstallerPackageFor(pkg.Name).StartExperiment(ctx, pkg.Version, tmpDir)
}

// RemoveExperiment removes an experiment.
func (i *installerImpl) RemoveExperiment(ctx context.Context, pkg string) error {
	i.m.Lock()
	defer i.m.Unlock()
	return i.getInstallerPackageFor(pkg).StopExperiment(ctx)
}

// PromoteExperiment promotes an experiment to stable.
func (i *installerImpl) PromoteExperiment(ctx context.Context, pkg string) error {
	i.m.Lock()
	defer i.m.Unlock()
	return i.getInstallerPackageFor(pkg).PromoteExperiment(ctx)
}

// Purge removes all packages.
func (i *installerImpl) Purge(ctx context.Context) {
	i.m.Lock()
	defer i.m.Unlock()

	packages, err := i.db.ListPackages()
	if err != nil {
		// if we can't list packages we'll only remove the installer
		packages = nil
		log.Warnf("could not list packages: %v", err)
	}
	for _, pkg := range packages {
		if pkg.Name == packageDatadogInstaller {
			continue
		}
		err := i.getInstallerPackageFor(pkg.Name).RemovePackage(ctx)
		if err != nil {
			log.Warnf("could not remove package %s: %v", pkg.Name, err)
		}
	}
	err = i.getInstallerPackageFor(packageDatadogInstaller).RemovePackage(ctx)
	if err != nil {
		log.Warnf("could not remove installer: %v", err)
	}

	// remove all from disk
	span, _ := tracer.StartSpanFromContext(ctx, "remove_all")
	err = os.RemoveAll(paths.PackagesPath)
	defer span.Finish(tracer.WithError(err))
	if err != nil {
		log.Warnf("could not remove path: %v", err)
	}
}

// Remove uninstalls a package.
func (i *installerImpl) Remove(ctx context.Context, pkg string) error {
	i.m.Lock()
	defer i.m.Unlock()
	err := i.getInstallerPackageFor(pkg).RemovePackage(ctx)
	if err != nil {
		return fmt.Errorf("could not remove package: %w", err)
	}
	err = i.repositories.Delete(ctx, pkg)
	if err != nil {
		return fmt.Errorf("could not delete repository: %w", err)
	}
	err = i.db.DeletePackage(pkg)
	if err != nil {
		return fmt.Errorf("could not remove package installation in db: %w", err)
	}
	return nil
}

// GarbageCollect removes unused packages.
func (i *installerImpl) GarbageCollect(ctx context.Context) error {
	i.m.Lock()
	defer i.m.Unlock()

	return i.repositories.Cleanup(ctx)
}

// InstrumentAPMInjector instruments the APM injector.
func (i *installerImpl) InstrumentAPMInjector(ctx context.Context, method string) error {
	i.m.Lock()
	defer i.m.Unlock()

	injectorInstalled, err := i.IsInstalled(ctx, packageAPMInjector)
	if err != nil {
		return fmt.Errorf("could not check if APM injector is installed: %w", err)
	}
	if !injectorInstalled {
		return fmt.Errorf("APM injector is not installed")
	}

	err = service.InstrumentAPMInjector(ctx, method)
	if err != nil {
		return fmt.Errorf("could not instrument APM: %w", err)
	}
	return nil
}

// UninstrumentAPMInjector instruments the APM injector.
func (i *installerImpl) UninstrumentAPMInjector(ctx context.Context, method string) error {
	i.m.Lock()
	defer i.m.Unlock()

	injectorInstalled, err := i.IsInstalled(ctx, packageAPMInjector)
	if err != nil {
		return fmt.Errorf("could not check if APM injector is installed: %w", err)
	}
	if !injectorInstalled {
		return fmt.Errorf("APM injector is not installed")
	}

	err = service.UninstrumentAPMInjector(ctx, method)
	if err != nil {
		return fmt.Errorf("could not instrument APM: %w", err)
	}
	return nil
}

const (
	packageUnknownSize = 2 << 30  // 2GiB
	installerOverhead  = 10 << 20 // 10MiB
)

// checkAvailableDiskSpace checks if there is enough disk space to install a package at the given path.
// This will check the underlying partition of the given path. Note that the path must be an existing dir.
//
// On Unix, it is computed using `statfs` and is the number of free blocks available to an unprivileged used * block size
// See https://man7.org/linux/man-pages/man2/statfs.2.html for more details
// On Windows, it is computed using `GetDiskFreeSpaceExW` and is the number of bytes available
// See https://docs.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getdiskfreespaceexw for more details
func checkAvailableDiskSpace(pkg *oci.DownloadedPackage, path string) error {
	requiredDiskSpace := pkg.Size
	if requiredDiskSpace == 0 {
		requiredDiskSpace = packageUnknownSize
	}
	requiredDiskSpace += installerOverhead

	_, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("could not stat path %s: %w", path, err)
	}
	s, err := fsDisk.GetUsage(path)
	if err != nil {
		return err
	}
	if s.Available < uint64(requiredDiskSpace) {
		return fmt.Errorf("not enough disk space at %s: %d bytes available, %d bytes required", path, s.Available, requiredDiskSpace)
	}
	return nil
}

func ensurePackageDirExists() error {
	err := os.MkdirAll(paths.PackagesPath, 0755)
	if err != nil {
		return fmt.Errorf("error creating packages directory: %w", err)
	}
	return nil
}
