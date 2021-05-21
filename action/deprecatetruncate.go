package action

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/operator-framework/operator-registry/public/declcfg"
	"github.com/operator-framework/operator-registry/public/property"
	"github.com/sirupsen/logrus"
)

type DeprecateTruncate struct {
	FromDir     string
	BundleImage string

	Log logrus.Logger
}

func (d DeprecateTruncate) Run(ctx context.Context) error {
	//
	// Make sure root declarative config directory exists
	//
	if err := ensureDir(d.FromDir); err != nil {
		return fmt.Errorf("ensure root declarative config directory %q: %v", d.FromDir, err)
	}

	d.Log.Infof("Loading declarative configs")
	fromCfg, err := declcfg.LoadDir(d.FromDir)
	if err != nil {
		return fmt.Errorf("load declarative configs: %v", err)
	}

	var depBundle *declcfg.Bundle
	for _, b := range fromCfg.Bundles {
		if b.Image == d.BundleImage {
			depBundle = &b
			break
		}
	}
	if depBundle == nil {
		return fmt.Errorf("could not find bundle in index with image %q", d.BundleImage)
	}
	for _, p := range depBundle.Properties {
		if p.Type == "olm.deprecated" {
			return fmt.Errorf("bundle %q is already deprecated", depBundle.Name)
		}
	}

	bundlePackage := depBundle.Package
	packageCfg := &declcfg.DeclarativeConfig{}
	for _, p := range fromCfg.Packages {
		if p.Name == bundlePackage {
			packageCfg.Packages = []declcfg.Package{p}
			break
		}
	}

	bundleMap := map[string]bundleProps{}
	for _, b := range fromCfg.Bundles {
		b := b
		if b.Package == bundlePackage {
			packageCfg.Bundles = append(packageCfg.Bundles, b)
		}
		props, err := property.Parse(b.Properties)
		if err != nil {
			return fmt.Errorf("parse properties for bundle %q: %v", b.Name, err)
		}
		bundleMap[b.Name] = bundleProps{&b, *props}
	}

	heads, err := getHeads(bundleMap)
	if err != nil {
		return fmt.Errorf("get channel heads: %v", err)
	}

	d.Log.Infof("Deprecating bundle %q", depBundle.Name)
	type deprecated struct{}
	v := &deprecated{}
	property.AddToScheme("olm.deprecated", v)
	depBundleProps := bundleMap[depBundle.Name]
	depBundleProps.Bundle.Properties = append(depBundleProps.Bundle.Properties, property.MustBuild(v))

	toRemove := bundlePropsSet{}
	for _, ch := range depBundleProps.Channels {
		if ch.Replaces != "" {
			rep, ok := bundleMap[ch.Replaces]
			if !ok {
				continue
			}
			toRemove.insert(rep.Name, rep)
		}
	}

	for cur := toRemove.pop(); cur != nil; cur = toRemove.pop() {
		if _, ok := heads[cur.Name]; ok {
			return fmt.Errorf("cannot remove channel head %q", cur.Name)
		}
		for _, ch := range cur.Channels {
			if ch.Replaces != "" {
				rep, ok := bundleMap[ch.Replaces]
				if !ok {
					continue
				}
				toRemove.insert(rep.Name, rep)
			}
		}
		for _, skip := range cur.Skips {
			if string(skip) != "" {
				sk, ok := bundleMap[string(skip)]
				if !ok {
					continue
				}
				toRemove.insert(sk.Name, sk)
			}
		}
		d.Log.Infof("Removing bundle %q and its incoming replaces edges", cur.Name)
		if err := removeReplacesFor(bundleMap, cur.Name); err != nil {
			return err
		}
		delete(bundleMap, cur.Name)
	}

	fromCfg.Bundles = []declcfg.Bundle{}
	for _, b := range bundleMap {
		b.Bundle.Properties = property.Deduplicate(b.Bundle.Properties)
		fromCfg.Bundles = append(fromCfg.Bundles, *b.Bundle)
	}
	sort.Slice(fromCfg.Bundles, func(i, j int) bool {
		return fromCfg.Bundles[i].Name < fromCfg.Bundles[j].Name
	})
	packageDir := filepath.Join(d.FromDir, bundlePackage)
	d.Log.Infof("Writing updated declarative config for package %q", bundlePackage)
	return writeCfg(*fromCfg, packageDir)
}

type bundlePropsSet map[string]bundleProps

func (s bundlePropsSet) insert(key string, value bundleProps) {
	s[key] = value
}

func (s bundlePropsSet) pop() *bundleProps {
	for k, v := range s {
		delete(s, k)
		return &v
	}
	return nil
}
