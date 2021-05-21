package action

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/operator-framework/operator-registry/public/action"
	"github.com/operator-framework/operator-registry/public/declcfg"
	"github.com/operator-framework/operator-registry/public/property"
	"github.com/sirupsen/logrus"
)

type Add struct {
	FromDir     string
	BundleImage string

	OverwriteLatest bool
	Log             logrus.Logger
}

func (a Add) Run(ctx context.Context) error {
	//
	// Make sure root declarative config directory exists
	//
	if err := ensureDir(a.FromDir); err != nil {
		return fmt.Errorf("ensure root declarative config directory %q: %v", a.FromDir, err)
	}

	//
	// Create a registry to pull and unpack images.
	//
	reg, err := newRegistry()
	if err != nil {
		return fmt.Errorf("create temporary image registry: %v", err)
	}
	defer destroyRegistry(reg, a.Log)

	//
	// Render bundle image
	//
	addBundle, err := a.renderBundle(ctx, reg, a.BundleImage)
	if err != nil {
		return fmt.Errorf("render bundle image as declarative config: %v", err)
	}

	//
	// Load declarative config that we want to add to.
	//
	packageDir := filepath.Join(a.FromDir, addBundle.Package)
	if err := ensureDir(packageDir); err != nil {
		return fmt.Errorf("ensure declarative package directory %q: %v", packageDir, err)
	}
	a.Log.Infof("Loading declarative configs for package %q", addBundle.Package)
	fromCfg, err := declcfg.LoadDir(packageDir)
	if err != nil {
		return fmt.Errorf("load declarative configs: %v", err)
	}

	switch l := len(fromCfg.Packages); l {
	case 0:
		a.Log.Infof("Initializing new package %q for bundle %q", addBundle.Package, addBundle.Name)
		fromCfg, err = initDeclcfgFromBundle(ctx, reg, *addBundle)
	case 1:
		a.Log.Infof("Adding bundle %q to existing package %q", addBundle.Name, addBundle.Package)
		bundleMap := map[string]bundleProps{}
		for _, b := range fromCfg.Bundles {
			b := b
			if _, ok := bundleMap[b.Name]; ok {
				return fmt.Errorf("duplicate bundle %q", b.Name)
			}
			if b.Package != addBundle.Package {
				return fmt.Errorf("found package %q in bundle %q, expected %q", b.Package, b.Name, addBundle.Package)
			}
			props, err := property.Parse(b.Properties)
			if err != nil {
				return fmt.Errorf("parse properties for bundle %q: %v", b.Name, err)
			}
			bundleMap[b.Name] = bundleProps{Bundle: &b, Properties: *props}
		}
		if _, ok := bundleMap[addBundle.Name]; ok {
			for _, b := range bundleMap {
				for _, ch := range b.Channels {
					if ch.Replaces == addBundle.Name {
						return fmt.Errorf("can't overwrite bundle %q, it is replaced by bundle %q", addBundle.Name, b.Name)
					}
				}
				for _, skips := range b.Skips {
					if string(skips) == addBundle.Name {
						return fmt.Errorf("can't overwrite bundle %q, it is skipped by bundle %q", addBundle.Name, b.Name)
					}
				}
			}
			if !a.OverwriteLatest {
				return fmt.Errorf("can't overwrite bundle %q, --overwrite-latest not enabled", addBundle.Name)
			}
		}
		props, err := property.Parse(addBundle.Properties)
		if err != nil {
			return fmt.Errorf("parse properties for bundle %q: %v", addBundle.Name, err)
		}
		bundleMap[addBundle.Name] = bundleProps{Bundle: addBundle, Properties: *props}

		heads, err := getHeads(bundleMap)
		if err != nil {
			return fmt.Errorf("get channel heads: %v", err)
		}

		a.Log.Infof("Adding channels to descendents across package %q", addBundle.Package)
		for _, head := range heads {
			a.addChannelsToDescendents(bundleMap, head)
		}

		fromCfg.Bundles = []declcfg.Bundle{}
		for _, b := range bundleMap {
			b.Bundle.Properties = property.Deduplicate(b.Bundle.Properties)
			fromCfg.Bundles = append(fromCfg.Bundles, *b.Bundle)
		}
		sort.Slice(fromCfg.Bundles, func(i, j int) bool {
			return fromCfg.Bundles[i].Name < fromCfg.Bundles[j].Name
		})
		defCh, err := defaultChannel(ctx, reg, bundleMap)
		if err != nil {
			return fmt.Errorf("set default channel: %v", err)
		}
		if fromCfg.Packages[0].DefaultChannel != defCh {
			a.Log.Infof("Updating default channel for package %q to %q", addBundle.Package, defCh)
			fromCfg.Packages[0].DefaultChannel = defCh
		}
	default:
		return fmt.Errorf("found %d olm.package blobs in %q, expected 1", len(fromCfg.Packages), packageDir)
	}
	a.Log.Infof("Writing updated declarative config for package %q", addBundle.Package)
	return writeCfg(*fromCfg, packageDir)
}

func (a Add) renderBundle(ctx context.Context, reg image.Registry, image string) (*declcfg.Bundle, error) {
	renderer := action.Render{
		Refs:     []string{image},
		Registry: reg,
	}
	a.Log.Infof("Rendering bundle image %q as declarative config", image)
	bundleCfg, err := renderer.Run(ctx)
	if err != nil {
		return nil, fmt.Errorf("render bundle image as declarative config: %v", err)
	}
	return &bundleCfg.Bundles[0], nil
}

func initDeclcfgFromBundle(ctx context.Context, reg image.Registry, bundle declcfg.Bundle) (*declcfg.DeclarativeConfig, error) {
	tmpDir, err := os.MkdirTemp("", "dcm-unpack-bundle-")
	if err != nil {
		return nil, fmt.Errorf("create unpack directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ref := image.SimpleReference(bundle.Image)
	if err := reg.Unpack(ctx, ref, tmpDir); err != nil {
		return nil, fmt.Errorf("unpack image %q: %v", ref, err)
	}
	imageInput, err := registry.NewImageInput(ref, tmpDir)
	if err != nil {
		return nil, fmt.Errorf("load bundle: %v", err)
	}
	init := action.Init{
		Package:        imageInput.Bundle.Package,
		DefaultChannel: imageInput.AnnotationsFile.GetDefaultChannelName(),
	}
	icons, err := imageInput.Bundle.Icons()
	if err != nil {
		return nil, fmt.Errorf("get icons: %v", err)
	}
	if len(icons) > 0 {
		init.IconReader = bytes.NewBuffer(icons[0].Base64data)
	}

	description, err := imageInput.Bundle.Description()
	if err != nil {
		return nil, fmt.Errorf("get description: %v", err)
	}
	if len(description) > 0 {
		init.DescriptionReader = bytes.NewBufferString(description)
	}

	pkg, err := init.Run()
	if err != nil {
		return nil, err
	}
	return &declcfg.DeclarativeConfig{
		Packages: []declcfg.Package{*pkg},
		Bundles:  []declcfg.Bundle{bundle},
	}, nil
}

func (a Add) addChannelsToDescendents(bundleMap map[string]bundleProps, cur bundleProps) {
	for _, ch := range cur.Channels {
		next, ok := bundleMap[ch.Replaces]
		if !ok {
			continue
		}
		if len(next.Channels) == 0 {
			continue
		}
		addCh := property.Channel{ch.Name, next.Channels[0].Replaces}
		found := false
		for _, nch := range next.Channels {
			if nch == addCh {
				found = true
				break
			}
		}
		if !found {
			next.Bundle.Properties = append(next.Bundle.Properties, property.MustBuildChannel(ch.Name, next.Channels[0].Replaces))
			next.Channels = append(next.Channels, addCh)
		}
		a.addChannelsToDescendents(bundleMap, next)
	}
}
